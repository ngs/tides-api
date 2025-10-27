// Package main fits harmonic constituents to JMA tide station observation data.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"go.ngs.io/tides-api/internal/domain"
	"go.ngs.io/tides-api/internal/jma"
)

type sample struct {
	Time   time.Time
	Height float64
}

type overrideConstituent struct {
	Name       string  `json:"name"`
	AmplitudeM float64 `json:"amplitude_m"`
	PhaseDeg   float64 `json:"phase_deg"`
}

type stationOverride struct {
	Name         string                `json:"name"`
	Station      string                `json:"station"`
	Lat          float64               `json:"lat"`
	Lon          float64               `json:"lon"`
	RadiusKm     float64               `json:"radius_km"`
	DatumOffset  float64               `json:"datum_offset_m"`
	Constituents []overrideConstituent `json:"constituents"`
	Source       string                `json:"source"`
}

func main() {
	var (
		jmaPath     string
		station     string
		stationName string
		lat         float64
		lon         float64
		radiusKm    float64
		minDateStr  string
		maxDateStr  string
		constCSV    string
	)

	flag.StringVar(&jmaPath, "jma_file", "", "Path or URL to JMA TXT file")
	flag.StringVar(&station, "station", "", "JMA station code (e.g., KZ)")
	flag.StringVar(&stationName, "name", "", "Human-friendly station name for metadata")
	flag.Float64Var(&lat, "lat", 0, "Latitude in degrees")
	flag.Float64Var(&lon, "lon", 0, "Longitude in degrees (east positive)")
	flag.Float64Var(&radiusKm, "radius_km", 40, "Radius in km within which to apply these overrides")
	flag.StringVar(&minDateStr, "start_date", "", "Optional start date (YYYY-MM-DD, JST)")
	flag.StringVar(&maxDateStr, "end_date", "", "Optional end date (YYYY-MM-DD, JST)")
	flag.StringVar(&constCSV, "constituents", "M2,S2,N2,K2,K1,O1,P1,Q1,M4,MS4,MN4,M6,S4,Mf,Mm,Ssa,Sa", "Comma-separated constituent list")
	flag.Parse()

	if jmaPath == "" || station == "" {
		fmt.Fprintln(os.Stderr, "Usage: jma-harmonics -jma_file <path|url> -station KZ -lat 35.3 -lon 139.9 [options]")
		os.Exit(2)
	}

	records, err := jma.LoadStationRecordsFromPath(jmaPath, station)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load JMA data: %v\n", err)
		os.Exit(1)
	}

	minDate, maxDate, err := parseDateRange(minDateStr, maxDateStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	samples := extractSamples(records, minDate, maxDate)
	if len(samples) == 0 {
		fmt.Fprintln(os.Stderr, "no valid hourly samples in the requested window")
		os.Exit(1)
	}

	constituents := parseConstituents(constCSV)
	if len(constituents) == 0 {
		fmt.Fprintln(os.Stderr, "no constituents provided")
		os.Exit(1)
	}

	intercept, overrides, err := fitHarmonics(samples, lon, constituents)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fit failed: %v\n", err)
		os.Exit(1)
	}

	if stationName == "" {
		stationName = station
	}

	payload := stationOverride{
		Name:         stationName,
		Station:      station,
		Lat:          lat,
		Lon:          lon,
		RadiusKm:     radiusKm,
		DatumOffset:  intercept,
		Constituents: overrides,
		Source:       "jma-harmonics",
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(payload); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode JSON: %v\n", err)
		os.Exit(1)
	}
}

func parseDateRange(minStr, maxStr string) (time.Time, time.Time, error) {
	var minTime, maxTime time.Time
	var err error
	if minStr != "" {
		minTime, err = time.ParseInLocation("2006-01-02", minStr, jma.JSTLocation)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start_date: %w", err)
		}
	}
	if maxStr != "" {
		maxTime, err = time.ParseInLocation("2006-01-02", maxStr, jma.JSTLocation)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end_date: %w", err)
		}
		maxTime = maxTime.Add(24 * time.Hour)
	}
	return minTime, maxTime, nil
}

func extractSamples(records []jma.HourlyRecord, minTime, maxTime time.Time) []sample {
	samples := make([]sample, 0, len(records)*24)
	for _, rec := range records {
		dayStart := rec.Time
		dayEnd := dayStart.Add(24 * time.Hour)
		if !minTime.IsZero() && dayEnd.Before(minTime) {
			continue
		}
		if !maxTime.IsZero() && dayStart.After(maxTime) {
			continue
		}
		for hour := 0; hour < 24; hour++ {
			if !rec.Valid[hour] {
				continue
			}
			jst := dayStart.Add(time.Duration(hour) * time.Hour)
			if !minTime.IsZero() && jst.Before(minTime) {
				continue
			}
			if !maxTime.IsZero() && !jst.Before(maxTime) {
				continue
			}
			samples = append(samples, sample{
				Time:   jst.UTC(),
				Height: rec.Hourly[hour],
			})
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Time.Before(samples[j].Time) })
	return samples
}

func parseConstituents(csv string) []string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.ToUpper(strings.TrimSpace(p))
		if trimmed == "" {
			continue
		}
		if _, ok := domain.GetConstituentSpeed(trimmed); ok {
			out = append(out, trimmed)
		}
	}
	return out
}

func fitHarmonics(samples []sample, lon float64, names []string) (float64, []overrideConstituent, error) {
	speeds := make([]float64, len(names))
	for i, name := range names {
		speed, ok := domain.GetConstituentSpeed(name)
		if !ok {
			return 0, nil, fmt.Errorf("unknown constituent: %s", name)
		}
		speeds[i] = speed
	}

	nodal := domain.NewAstronomicalNodalCorrection()
	ref := time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)
	paramCount := 1 + len(names)*2

	normal := make([][]float64, paramCount)
	for i := range normal {
		normal[i] = make([]float64, paramCount)
	}
	rhs := make([]float64, paramCount)

	for _, s := range samples {
		deltaHours := s.Time.Sub(ref).Hours()
		features := make([]float64, paramCount)
		features[0] = 1
		idx := 1
		for i, name := range names {
			f, u := nodal.GetFactors(name, deltaHours)
			thetaDeg := speeds[i]*deltaHours + lon + u
			thetaRad := domain.Deg2Rad(thetaDeg)
			cosTerm := f * math.Cos(thetaRad)
			sinTerm := f * math.Sin(thetaRad)
			features[idx] = cosTerm
			features[idx+1] = sinTerm
			idx += 2
		}
		for i := 0; i < paramCount; i++ {
			rhs[i] += features[i] * s.Height
			for j := 0; j <= i; j++ {
				normal[i][j] += features[i] * features[j]
			}
		}
	}

	for i := 0; i < paramCount; i++ {
		for j := 0; j < i; j++ {
			normal[j][i] = normal[i][j]
		}
	}

	coeffs, err := solveSPD(normal, rhs)
	if err != nil {
		return 0, nil, err
	}

	intercept := coeffs[0]
	overrides := make([]overrideConstituent, 0, len(names))
	idx := 1
	for _, name := range names {
		c := coeffs[idx]
		s := coeffs[idx+1]
		amp := math.Hypot(c, s)
		phase := math.Mod(domain.Rad2Deg(math.Atan2(s, c))+360, 360)
		overrides = append(overrides, overrideConstituent{
			Name:       name,
			AmplitudeM: round(amp, 6),
			PhaseDeg:   round(phase, 6),
		})
		idx += 2
	}

	return round(intercept, 6), overrides, nil
}

func solveSPD(mat [][]float64, rhs []float64) ([]float64, error) {
	n := len(rhs)
	L := make([][]float64, n)
	for i := range L {
		L[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := mat[i][j]
			for k := 0; k < j; k++ {
				sum -= L[i][k] * L[j][k]
			}
			if i == j {
				if sum <= 0 {
					return nil, fmt.Errorf("matrix not positive definite")
				}
				L[i][j] = math.Sqrt(sum)
			} else {
				L[i][j] = sum / L[j][j]
			}
		}
	}

	y := make([]float64, n)
	for i := 0; i < n; i++ {
		sum := rhs[i]
		for k := 0; k < i; k++ {
			sum -= L[i][k] * y[k]
		}
		y[i] = sum / L[i][i]
	}

	x := make([]float64, n)
	for i := n - 1; i >= 0; i-- {
		sum := y[i]
		for k := i + 1; k < n; k++ {
			sum -= L[k][i] * x[k]
		}
		x[i] = sum / L[i][i]
	}
	return x, nil
}

func round(v float64, places int) float64 {
	pow := math.Pow(10, float64(places))
	return math.Round(v*pow) / pow
}
