// Package main generates station override files for JMA tide stations.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type stationEntry struct {
	Code string `json:"code"`
	Lat  string `json:"lat"`
	Lng  string `json:"lng"`
}

type overrideResult struct {
	Name         string           `json:"name"`
	Station      string           `json:"station"`
	Lat          float64          `json:"lat"`
	Lon          float64          `json:"lon"`
	RadiusKm     float64          `json:"radius_km"`
	DatumOffset  float64          `json:"datum_offset_m"`
	Constituents []map[string]any `json:"constituents"`
	Source       string           `json:"source"`
}

type datumEntry struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	OffsetM float64 `json:"offset_m"`
}

func main() {
	stationsPath := flag.String("stations", "tmp/jma-stations.json", "Path to station metadata JSON (code/lat/lng)")
	txtDir := flag.String("txt_dir", "tmp/jma_txt", "Directory containing {CODE}.txt files")
	harmonicsBin := flag.String("harmonics_bin", "tmp/bin/jma-harmonics", "Path to jma-harmonics binary (built automatically if missing)")
	overridesOut := flag.String("overrides_out", "data/jma_station_overrides.json", "Output JSON for station overrides")
	datumOut := flag.String("datum_out", "data/jma_datum_offsets.json", "Output JSON for datum offsets")
	radiusKm := flag.Float64("radius_km", 40, "Default radius_km when jma-harmonics output omits it")
	flag.Parse()

	stations, err := loadStations(*stationsPath)
	if err != nil {
		exitErr(err)
	}
	if len(stations) == 0 {
		exitErr(fmt.Errorf("no stations found in %s", *stationsPath))
	}

	if err := ensureHarmonicsBinary(*harmonicsBin); err != nil {
		exitErr(fmt.Errorf("build jma-harmonics: %w", err))
	}

	txtDirAbs, err := filepath.Abs(*txtDir)
	if err != nil {
		exitErr(err)
	}

	overrides := make([]overrideResult, 0, len(stations))
	datumOffsets := make([]datumEntry, 0, len(stations))

	for idx, st := range stations {
		code := strings.TrimSpace(st.Code)
		if code == "" {
			continue
		}
		lat, err := parseCoordinate(st.Lat)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] skip %s: invalid latitude (%v)\n", idx+1, len(stations), code, err)
			continue
		}
		lon, err := parseCoordinate(st.Lng)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] skip %s: invalid longitude (%v)\n", idx+1, len(stations), code, err)
			continue
		}
		txtPath := filepath.Join(txtDirAbs, fmt.Sprintf("%s.txt", code))
		if _, err := os.Stat(txtPath); err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] skip %s: %v\n", idx+1, len(stations), code, err)
			continue
		}
		result, err := runHarmonics(*harmonicsBin, txtPath, code, lat, lon, *radiusKm)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%d/%d] harmonics failed for %s: %v\n", idx+1, len(stations), code, err)
			continue
		}
		overrides = append(overrides, result)
		datumOffsets = append(datumOffsets, datumEntry{
			Name:    result.Name,
			Lat:     result.Lat,
			Lon:     result.Lon,
			OffsetM: result.DatumOffset,
		})
		fmt.Printf("[%d/%d] processed %s\n", idx+1, len(stations), code)
	}

	if len(overrides) == 0 {
		exitErr(fmt.Errorf("no overrides produced"))
	}

	sort.Slice(overrides, func(i, j int) bool { return stationKey(overrides[i]) < stationKey(overrides[j]) })
	sort.Slice(datumOffsets, func(i, j int) bool { return datumOffsets[i].Name < datumOffsets[j].Name })

	if err := writeJSON(*overridesOut, overrides); err != nil {
		exitErr(err)
	}
	if err := writeJSON(*datumOut, datumOffsets); err != nil {
		exitErr(err)
	}

	fmt.Printf("Saved %d overrides -> %s\n", len(overrides), *overridesOut)
	fmt.Printf("Saved datum offsets -> %s\n", *datumOut)
}

func loadStations(path string) ([]stationEntry, error) {
	//nolint:gosec // G304: File path from command-line argument, user-controlled.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []stationEntry
	dec := json.NewDecoder(bufio.NewReader(f))
	if err := dec.Decode(&entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func ensureHarmonicsBinary(binPath string) error {
	if _, err := os.Stat(binPath); err == nil {
		return nil
	}
	//nolint:gosec // G301: Standard directory permissions for build output.
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", binPath, "./cmd/jma-harmonics")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runHarmonics(binPath, txtPath, code string, lat, lon, radius float64) (overrideResult, error) {
	args := []string{
		binPath,
		fmt.Sprintf("-jma_file=%s", txtPath),
		fmt.Sprintf("-station=%s", code),
		fmt.Sprintf("-name=%s", code),
		fmt.Sprintf("-lat=%f", lat),
		fmt.Sprintf("-lon=%f", lon),
		fmt.Sprintf("-radius_km=%f", radius),
	}
	//nolint:gosec // G204: args[0] is known binary path, args from controlled source.
	cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return overrideResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var result overrideResult
	if err := json.Unmarshal([]byte(stdout.String()), &result); err != nil {
		return overrideResult{}, err
	}
	return result, nil
}

func parseCoordinate(raw string) (float64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("empty coordinate")
	}
	sign := 1.0
	if strings.ContainsAny(s, "SWＷＳ南西") {
		sign = -1
	}
	replacer := strings.NewReplacer(
		"゜", " ", "°", " ", "º", " ", "˚", " ",
		"′", " ", "'", " ", "’", " ", "`", " ",
		"″", " ", "\"", " ",
		"N", " ", "E", " ", "S", " ", "W", " ",
	)
	cleaned := replacer.Replace(s)
	fields := strings.Fields(cleaned)
	values := make([]float64, 0, len(fields))
	for _, f := range fields {
		if v, err := strconv.ParseFloat(f, 64); err == nil {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		digits := strings.Builder{}
		for _, r := range s {
			if r >= '0' && r <= '9' {
				digits.WriteRune(r)
			}
		}
		if digits.Len() == 0 {
			return 0, fmt.Errorf("unable to parse coordinate: %s", raw)
		}
		num, _ := strconv.ParseFloat(digits.String(), 64)
		deg := mathFloor(num / 100)
		minutes := num - deg*100
		return sign * (deg + minutes/60), nil
	}
	deg := values[0]
	minutes := 0.0
	seconds := 0.0
	if len(values) > 1 {
		minutes = values[1]
	}
	if len(values) > 2 {
		seconds = values[2]
	}
	return sign * (deg + minutes/60 + seconds/3600), nil
}

func mathFloor(v float64) float64 {
	iv := int(v)
	if float64(iv) > v {
		return float64(iv - 1)
	}
	return float64(iv)
}

func stationKey(o overrideResult) string {
	if o.Station != "" {
		return o.Station
	}
	return o.Name
}

func writeJSON(path string, data any) error {
	//nolint:gosec // G301: Standard directory permissions for data output.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	//nolint:gosec // G304: File path from function parameter, controlled by caller.
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
