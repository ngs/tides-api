// Command jma-compare parses JMA fixed-width tide text and compares it
// against the API predictions for a given day, reporting the mean offset
// (recommended datum_offset_m) and RMSE around that mean.
//
// JMA observations are chart-datum (DL) referenced. The API returns
// MSL-centred heights by default (datum=MSL), so the reported mean(JMA-API)
// equals the chart datum offset (Z0 below MSL) - which is exactly the
// recommended datum_offset_m. The RMSE is computed around that mean and is
// therefore independent of the datum convention. To instead check absolute
// agreement against DL, request the API with datum=CD in -api_url, which makes
// the mean collapse toward 0.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"time"

	"go.ngs.io/tides-api/internal/jma"
)

type apiPrediction struct {
	Time    string  `json:"time"`
	HeightM float64 `json:"height_m"`
}

type apiResponse struct {
	Predictions []apiPrediction `json:"predictions"`
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("HTTP %d (failed to read body: %v)", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

// findTargetRecord extracts hourly heights for the specified JST date.
func findTargetRecord(records []jma.HourlyRecord, dateStr string) ([]float64, error) {
	target, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid date format: %v", err)
	}
	locDate := time.Date(target.Year(), target.Month(), target.Day(), 0, 0, 0, 0, jma.JSTLocation)
	for _, rec := range records {
		if rec.Time.Equal(locDate) {
			hours := make([]float64, 24)
			for i := 0; i < 24; i++ {
				if rec.Valid[i] {
					hours[i] = rec.Hourly[i]
				} else {
					// Mark missing observations (JMA "999") as NaN so they
					// are excluded from the comparison instead of being
					// treated as a 0.0m tide height.
					hours[i] = math.NaN()
				}
			}
			return hours, nil
		}
	}
	return nil, fmt.Errorf("JMA record not found for date %s", dateStr)
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
		// Skip missing JMA observations; they must not enter the statistics.
		if math.IsNaN(hourly[i]) {
			continue
		}
		t := start.Add(time.Duration(i) * time.Hour).Format(time.RFC3339)
		apiH, ok := apiMap[t]
		if !ok {
			return nil, fmt.Errorf("API missing time: %s", t)
		}
		diffs = append(diffs, hourly[i]-apiH)
	}
	if len(diffs) == 0 {
		return nil, fmt.Errorf("no valid JMA observations to compare")
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
		rmse = math.Sqrt(sse / float64(len(diffs)))
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
	)
	flag.StringVar(&jmaPath, "jma_file", "", "Path or URL to JMA TXT (fixed-width)")
	flag.StringVar(&station, "station", "KZ", "JMA station code (e.g., KZ)")
	flag.StringVar(&dateStr, "date", "2025-10-27", "Target date in JST (YYYY-MM-DD)")
	flag.StringVar(&apiURL, "api_url", "", "Full API URL to fetch predictions (must span the JST day; include params)")
	flag.StringVar(&startUTC, "start_utc", "2025-10-26T15:00:00Z", "Start time in UTC matching JST 00:00")
	flag.Parse()

	if jmaPath == "" || apiURL == "" {
		fmt.Fprintln(os.Stderr, "Usage: jma-compare -jma_file <path|url> -station KZ -date 2025-10-27 -api_url <url> [-start_utc ...]")
		os.Exit(2)
	}

	// Load JMA file.
	records, err := jma.LoadStationRecordsFromPath(jmaPath, station)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load JMA: %v\n", err)
		os.Exit(1)
	}

	// Find target line.
	hourly, err := findTargetRecord(records, dateStr)
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
