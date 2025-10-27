package domain

import (
	"math"
	"testing"
	"time"
)

// TestCalculateTideHeight_SingleConstituent tests tide calculation with a single constituent.
func TestCalculateTideHeight_SingleConstituent(t *testing.T) {
	// Use M2 constituent with known parameters
	// M2 speed: 28.9841042 deg/hr
	// Period: 12.4206012 hours

	refTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	params := PredictionParams{
		Constituents: []ConstituentParam{
			{
				Name:          "M2",
				AmplitudeM:    1.0,
				PhaseDeg:      0.0, // Phase at reference time
				SpeedDegPerHr: 28.9841042,
			},
		},
		MSL:             0.0,
		NodalCorrection: &IdentityNodalCorrection{},
		ReferenceTime:   refTime,
	}

	// Test at reference time (t=0)
	// Expected: A * cos(0) = 1.0
	h0 := CalculateTideHeight(refTime, params)
	if math.Abs(h0-1.0) > 1e-9 {
		t.Errorf("Height at t=0: expected 1.0, got %.10f", h0)
	}

	// Test at quarter period (should be near zero)
	// Quarter period = 12.4206012 / 4 = 3.10515 hours
	quarterPeriodHours := 3.10515
	quarterPeriod := time.Duration(quarterPeriodHours * float64(time.Hour))
	tQuarter := refTime.Add(quarterPeriod)
	hQuarter := CalculateTideHeight(tQuarter, params)

	// At quarter period, phase = 90 degrees, cos(90) = 0
	if math.Abs(hQuarter) > 1e-6 {
		t.Errorf("Height at quarter period: expected ~0, got %.10f", hQuarter)
	}

	// Test at half period (should be negative amplitude)
	halfPeriodHours := 6.2103
	halfPeriod := time.Duration(halfPeriodHours * float64(time.Hour))
	tHalf := refTime.Add(halfPeriod)
	hHalf := CalculateTideHeight(tHalf, params)

	// At half period, phase = 180 degrees, cos(180) = -1
	if math.Abs(hHalf-(-1.0)) > 1e-6 {
		t.Errorf("Height at half period: expected -1.0, got %.10f", hHalf)
	}
}

// TestCalculateTideHeight_MultipleConstituents tests with multiple constituents.
func TestCalculateTideHeight_MultipleConstituents(t *testing.T) {
	refTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	params := PredictionParams{
		Constituents: []ConstituentParam{
			{
				Name:          "M2",
				AmplitudeM:    0.5,
				PhaseDeg:      0.0,
				SpeedDegPerHr: 28.9841042,
			},
			{
				Name:          "S2",
				AmplitudeM:    0.2,
				PhaseDeg:      0.0,
				SpeedDegPerHr: 30.0,
			},
		},
		MSL:             0.0,
		NodalCorrection: &IdentityNodalCorrection{},
		ReferenceTime:   refTime,
	}

	// At t=0, both constituents should be at max
	// Expected: 0.5 + 0.2 = 0.7
	h0 := CalculateTideHeight(refTime, params)
	if math.Abs(h0-0.7) > 1e-9 {
		t.Errorf("Height at t=0: expected 0.7, got %.10f", h0)
	}
}

// TestFindExtrema tests extrema detection.
func TestFindExtrema(t *testing.T) {
	// Create a simple sinusoidal pattern with known extrema
	refTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	predictions := []TideLevel{
		{Time: refTime, HeightM: 0.0},
		{Time: refTime.Add(1 * time.Hour), HeightM: 0.5},
		{Time: refTime.Add(2 * time.Hour), HeightM: 0.9},
		{Time: refTime.Add(3 * time.Hour), HeightM: 1.0}, // High
		{Time: refTime.Add(4 * time.Hour), HeightM: 0.9},
		{Time: refTime.Add(5 * time.Hour), HeightM: 0.5},
		{Time: refTime.Add(6 * time.Hour), HeightM: 0.0},
		{Time: refTime.Add(7 * time.Hour), HeightM: -0.5},
		{Time: refTime.Add(8 * time.Hour), HeightM: -0.9},
		{Time: refTime.Add(9 * time.Hour), HeightM: -1.0}, // Low
		{Time: refTime.Add(10 * time.Hour), HeightM: -0.9},
		{Time: refTime.Add(11 * time.Hour), HeightM: -0.5},
		{Time: refTime.Add(12 * time.Hour), HeightM: 0.0},
	}

	extrema := FindExtrema(predictions)

	// Should find 1 high and 1 low
	if len(extrema.Highs) != 1 {
		t.Errorf("Expected 1 high, found %d", len(extrema.Highs))
	}

	if len(extrema.Lows) != 1 {
		t.Errorf("Expected 1 low, found %d", len(extrema.Lows))
	}

	// Verify high tide
	if len(extrema.Highs) > 0 {
		high := extrema.Highs[0]
		expectedTime := refTime.Add(3 * time.Hour)
		if !high.Time.Equal(expectedTime) {
			t.Errorf("High tide time: expected %v, got %v", expectedTime, high.Time)
		}
		if math.Abs(high.HeightM-1.0) > 1e-9 {
			t.Errorf("High tide height: expected 1.0, got %.10f", high.HeightM)
		}
	}

	// Verify low tide
	if len(extrema.Lows) > 0 {
		low := extrema.Lows[0]
		expectedTime := refTime.Add(9 * time.Hour)
		if !low.Time.Equal(expectedTime) {
			t.Errorf("Low tide time: expected %v, got %v", expectedTime, low.Time)
		}
		if math.Abs(low.HeightM-(-1.0)) > 1e-9 {
			t.Errorf("Low tide height: expected -1.0, got %.10f", low.HeightM)
		}
	}
}

// TestGeneratePredictions tests time series generation.
func TestGeneratePredictions(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC)
	interval := 30 * time.Minute

	params := PredictionParams{
		Constituents: []ConstituentParam{
			{
				Name:          "M2",
				AmplitudeM:    1.0,
				PhaseDeg:      0.0,
				SpeedDegPerHr: 28.9841042,
			},
		},
		MSL:             0.0,
		NodalCorrection: &IdentityNodalCorrection{},
		ReferenceTime:   start,
	}

	predictions := GeneratePredictions(start, end, interval, params)

	// Should have 5 points: 0:00, 0:30, 1:00, 1:30, 2:00
	expectedCount := 5
	if len(predictions) != expectedCount {
		t.Errorf("Expected %d predictions, got %d", expectedCount, len(predictions))
	}

	// Verify times
	for i, p := range predictions {
		expectedTime := start.Add(time.Duration(i) * interval)
		if !p.Time.Equal(expectedTime) {
			t.Errorf("Prediction %d: expected time %v, got %v", i, expectedTime, p.Time)
		}
	}
}

// TestDeg2Rad tests degree to radian conversion.
func TestDeg2Rad(t *testing.T) {
	tests := []struct {
		deg      float64
		expected float64
	}{
		{0, 0},
		{90, math.Pi / 2},
		{180, math.Pi},
		{360, 2 * math.Pi},
		{-90, -math.Pi / 2},
	}

	for _, tt := range tests {
		result := Deg2Rad(tt.deg)
		if math.Abs(result-tt.expected) > 1e-9 {
			t.Errorf("Deg2Rad(%.1f): expected %.10f, got %.10f", tt.deg, tt.expected, result)
		}
	}
}
