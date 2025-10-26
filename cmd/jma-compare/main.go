// Command jma-compare parses JMA fixed-width tide text and compares it
// against the API predictions for a given day, reporting the mean offset
// (recommended datum_offset_m) and RMSE around that mean.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type apiPrediction struct {
	Time    string  `json:"time"`
	HeightM float64 `json:"height_m"`
}

type apiResponse struct {
	Predictions []apiPrediction `json:"predictions"`
}

type jmaHourlyData struct {
	Year    int
	Month   int
	Day     int
	Station string
	Hourly  []float64
}

func fetch(url string) ([]byte, error) {
	// Use a client with timeout and explicit context to satisfy linters and be robust.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("HTTP %d (failed to read body: %v)", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

// parseJMAHourly parses a JMA fixed-width line and returns 24 hourly heights in meters.
// Spec: columns 1-72 -> 24 values of 3 chars each (cm), 73-78: YY MM DD, 79-80: station code.
func parseJMAHourly(line string) (*jmaHourlyData, error) {
	if len(line) < 80 {
		return nil, fmt.Errorf("line too short: %d", len(line))
	}
	hourly := make([]float64, 24)
	for i := 0; i < 24; i++ {
		start := 0 + 3*i
		end := start + 3
		if end > len(line) {
			return nil, fmt.Errorf("unexpected end of line while parsing hourly at %d", i)
		}
		raw := strings.TrimSpace(line[start:end])
		if raw == "" {
			raw = "999"
		}
		v, convErr := strconv.Atoi(raw)
		if convErr != nil {
			// Some lines may contain minus sign separated by space, try to fix.
			raw2 := strings.ReplaceAll(raw, " ", "")
			v, convErr = strconv.Atoi(raw2)
			if convErr != nil {
				return nil, fmt.Errorf("invalid hourly value '%s' at %d: %v", raw, i, convErr)
			}
		}
		if v == 999 {
			hourly[i] = 0 // Treat as missing; keep 0 to avoid NaN in stats.
		} else {
			hourly[i] = float64(v) / 100.0
		}
	}

	// Date.
	yStr := strings.TrimSpace(line[72:74])
	mStr := strings.TrimSpace(line[74:76])
	dStr := strings.TrimSpace(line[76:78])
	y, err := strconv.Atoi(yStr)
	if err != nil {
		return nil, fmt.Errorf("invalid year '%s': %v", yStr, err)
	}
	m, err := strconv.Atoi(mStr)
	if err != nil {
		return nil, fmt.Errorf("invalid month '%s': %v", mStr, err)
	}
	d, err := strconv.Atoi(dStr)
	if err != nil {
		return nil, fmt.Errorf("invalid day '%s': %v", dStr, err)
	}
	station := strings.TrimSpace(line[78:80])
	return &jmaHourlyData{
		Year:    y,
		Month:   m,
		Day:     d,
		Station: station,
		Hourly:  hourly,
	}, nil
}

// loadJMAData loads JMA data from a file or URL.
func loadJMAData(jmaPath string) ([]byte, error) {
	if strings.HasPrefix(jmaPath, "http://") || strings.HasPrefix(jmaPath, "https://") {
		return fetch(jmaPath)
	}
	return os.ReadFile(jmaPath)
}

// findTargetLine finds the target date line in JMA data.
func findTargetLine(data []byte, station, dateStr string) ([]float64, error) {
	target, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid date format: %v", err)
	}
	y2 := target.Year() % 100
	m2 := int(target.Month())
	d2 := target.Day()

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		jmaData, parseErr := parseJMAHourly(line)
		if parseErr != nil {
			continue
		}
		if jmaData.Station == station && jmaData.Year == y2 && jmaData.Month == m2 && jmaData.Day == d2 {
			return jmaData.Hourly, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan JMA file: %v", err)
	}
	return nil, fmt.Errorf("JMA line not found for station=%s date=%s", station, dateStr)
}

// fetchAPIData fetches and parses API data into a map.
func fetchAPIData(apiURL string) (map[string]float64, error) {
	body, err := fetch(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch API: %v", err)
	}
	var api apiResponse
	if err := json.Unmarshal(body, &api); err != nil {
		return nil, fmt.Errorf("invalid API JSON: %v", err)
	}

	apiMap := make(map[string]float64)
	for _, p := range api.Predictions {
		apiMap[p.Time] = p.HeightM
	}
	return apiMap, nil
}

// compareData compares hourly JMA data with API predictions.
func compareData(hourly []float64, apiMap map[string]float64, startUTC string) ([]float64, error) {
	start, err := time.Parse(time.RFC3339, startUTC)
	if err != nil {
		return nil, fmt.Errorf("invalid start_utc: %v", err)
	}

	diffs := make([]float64, 0, 24)
	for i := 0; i < 24; i++ {
		t := start.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
		apiH, ok := apiMap[t]
		if !ok {
			return nil, fmt.Errorf("API missing time: %s", t)
		}
		diffs = append(diffs, hourly[i]-apiH)
	}
	return diffs, nil
}

// calculateStats calculates mean and RMSE around mean.
func calculateStats(diffs []float64) (mean, rmse float64) {
	var sum float64
	for _, d := range diffs {
		sum += d
	}
	mean = sum / float64(len(diffs))

	var sse float64
	for _, d := range diffs {
		dd := d - mean
		sse += dd * dd
	}
	if len(diffs) > 0 {
		rmse = mathSqrt(sse / float64(len(diffs)))
	}
	return mean, rmse
}

func main() {
	var (
		jmaPath  string
		station  string
		dateStr  string
		apiURL   string
		startUTC string
		endUTC   string
	)
	flag.StringVar(&jmaPath, "jma_file", "", "Path or URL to JMA TXT (fixed-width)")
	flag.StringVar(&station, "station", "KZ", "JMA station code (e.g., KZ)")
	flag.StringVar(&dateStr, "date", "2025-10-27", "Target date in JST (YYYY-MM-DD)")
	flag.StringVar(&apiURL, "api_url", "", "Full API URL to fetch predictions (must span the JST day; include params)")
	flag.StringVar(&startUTC, "start_utc", "2025-10-26T15:00:00Z", "Start time in UTC matching JST 00:00")
	flag.StringVar(&endUTC, "end_utc", "2025-10-27T15:00:00Z", "End time in UTC matching JST 24:00")
	flag.Parse()

	if jmaPath == "" || apiURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: jma-compare -jma_file <path|url> -station KZ -date 2025-10-27 -api_url <url> [-start_utc ... -end_utc ...]")
		os.Exit(2)
	}

	// Load JMA file.
	data, err := loadJMAData(jmaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load JMA: %v\n", err)
		os.Exit(1)
	}

	// Find target line.
	hourly, err := findTargetLine(data, station, dateStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Fetch API.
	apiMap, err := fetchAPIData(apiURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Compare.
	diffs, err := compareData(hourly, apiMap, startUTC)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// Calculate statistics.
	mean, rmse := calculateStats(diffs)

	fmt.Printf("Paired points: %d\n", len(diffs))
	fmt.Printf("Mean(JMA-API) [m]: %.3f\n", mean)
	fmt.Printf("RMSE around mean [m]: %.3f\n", rmse)
	fmt.Printf("\nRecommended datum_offset_m: %.3f\n", mean)
}

func mathSqrt(v float64) float64 { // Avoid importing math for tiny tool size.
	// Newton-Raphson.
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 20; i++ {
		x = 0.5 * (x + v/x)
	}
	return x
}
