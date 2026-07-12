package main

import (
	"math"
	"testing"
	"time"

	"go.ngs.io/tides-api/internal/jma"
)

// Regression test for: findTargetRecord (main.go:62-68) maps hours with
// Valid=false to a tide height of 0.0m, so missing JMA observations are fed
// into the comparison statistics as if the sea level were exactly 0m,
// corrupting the mean offset and RMSE.
//
// Correct behavior: invalid (missing) hours must be excluded from the
// comparison. With 23 valid hours all differing from the API by exactly
// +0.5m and hour 5 missing, the resulting mean must be exactly 0.5 —
// not skewed by a bogus (0.0 - api) diff for the missing hour.
func TestMissingJMAHoursExcludedFromComparison(t *testing.T) {
	const (
		dateStr  = "2025-10-27"
		startUTC = "2025-10-26T15:00:00Z" // JST 2025-10-27 00:00
	)

	rec := jma.HourlyRecord{
		Station: "KZ",
		Time:    time.Date(2025, 10, 27, 0, 0, 0, 0, jma.JSTLocation),
	}
	for i := 0; i < 24; i++ {
		rec.Hourly[i] = 1.5
		rec.Valid[i] = true
	}
	// Hour 5 is a missing observation (JMA "999").
	rec.Hourly[5] = 0
	rec.Valid[5] = false

	hourly, err := findTargetRecord([]jma.HourlyRecord{rec}, dateStr)
	if err != nil {
		t.Fatalf("findTargetRecord: %v", err)
	}

	// API predicts a constant 1.0m for every hour of the day.
	start, err := time.Parse(time.RFC3339, startUTC)
	if err != nil {
		t.Fatalf("parse startUTC: %v", err)
	}
	apiMap := make(map[string]float64, 24)
	for i := 0; i < 24; i++ {
		apiMap[start.Add(time.Duration(i)*time.Hour).Format(time.RFC3339)] = 1.0
	}

	diffs, err := compareData(hourly, apiMap, startUTC)
	if err != nil {
		t.Fatalf("compareData: %v", err)
	}

	mean, rmse := calculateStats(diffs)

	// All 23 valid hours differ by exactly 1.5 - 1.0 = 0.5m; the missing
	// hour must not contribute a fake (0.0 - 1.0) = -1.0m diff.
	if math.IsNaN(mean) || math.Abs(mean-0.5) > 1e-9 {
		t.Errorf("mean(JMA-API) = %v, want 0.5 (missing hour treated as 0.0m tide height skews the mean; it must be excluded)", mean)
	}
	if math.IsNaN(rmse) || math.Abs(rmse) > 1e-9 {
		t.Errorf("RMSE around mean = %v, want 0 (all valid diffs are identical; nonzero RMSE means the missing hour leaked into the stats)", rmse)
	}

	// The missing hour must not appear in the diff series as a valid pair.
	for i, d := range diffs {
		if !math.IsNaN(d) && math.Abs(d-0.5) > 1e-9 {
			t.Errorf("diffs[%d] = %v, want 0.5 for every compared hour (missing hour must be excluded, not compared as 0.0m)", i, d)
		}
	}
}
