package usecase

import (
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"

	"go.ngs.io/tides-api/internal/domain"
)

// openOceanConstituents returns a spread of constituents so the Σ(H_M2+H_S2+
// H_K1+H_O1) chart datum fallback can be checked independently of the others.
func openOceanConstituents() []domain.ConstituentParam {
	return []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0.50, PhaseDeg: 0, SpeedDegPerHr: 28.9841042},
		{Name: "S2", AmplitudeM: 0.20, PhaseDeg: 0, SpeedDegPerHr: 30.0},
		{Name: "K1", AmplitudeM: 0.10, PhaseDeg: 0, SpeedDegPerHr: 15.0410686},
		{Name: "O1", AmplitudeM: 0.05, PhaseDeg: 0, SpeedDegPerHr: 13.9430356},
		{Name: "N2", AmplitudeM: 0.30, PhaseDeg: 0, SpeedDegPerHr: 28.4397295},
	}
}

// noAdjustmentEnv points the datum offset and station override tables at
// nonexistent files so an open-ocean location matches neither.
func noAdjustmentEnv(t *testing.T) {
	t.Helper()
	resetAdjustmentTables(t)
	dir := t.TempDir()
	t.Setenv("DATUM_OFFSETS_PATH", filepath.Join(dir, "none.json"))
	t.Setenv("STATION_OVERRIDES_PATH", filepath.Join(dir, "none.json"))
}

// With no override and no auto datum offset, the chart datum offset falls back
// to Σ(H_M2+H_S2+H_K1+H_O1); other constituents (N2 here) must not contribute.
func TestExecute_ChartDatumOffsetFallbackSumsPrincipalConstituents(t *testing.T) {
	noAdjustmentEnv(t)

	loader := &mockConstituentLoader{constituents: openOceanConstituents()}
	uc := NewPredictionUseCase(loader, loader, nil)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resp, err := uc.Execute(PredictionRequest{
		Lat:      ptrFloat(38.0),
		Lon:      ptrFloat(144.0), // Japan Trench: open ocean.
		Start:    start,
		End:      start.Add(time.Hour),
		Interval: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	const wantZ0 = 0.50 + 0.20 + 0.10 + 0.05
	if math.Abs(resp.ChartDatumOffsetM-wantZ0) > 1e-9 {
		t.Errorf("chart_datum_offset_m = %v, want %v (Σ of M2+S2+K1+O1 only)", resp.ChartDatumOffsetM, wantZ0)
	}
	if resp.MSL == nil || *resp.MSL != 0 {
		t.Errorf("msl_m = %v, want 0", resp.MSL)
	}
	if resp.Datum != datumMSL {
		t.Errorf("datum = %q, want MSL", resp.Datum)
	}
}

// datum=CD must add the chart datum offset to every predicted height and to the
// extrema, while datum=MSL leaves them 0-centred. The reported datum reflects
// what was applied.
func TestExecute_DatumCDShiftsHeightsAndExtrema(t *testing.T) {
	noAdjustmentEnv(t)

	loader := &mockConstituentLoader{constituents: openOceanConstituents()}
	uc := NewPredictionUseCase(loader, loader, nil)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base := PredictionRequest{
		Lat:      ptrFloat(38.0),
		Lon:      ptrFloat(144.0),
		Start:    start,
		End:      start.Add(48 * time.Hour),
		Interval: 30 * time.Minute,
	}

	mslResp, err := uc.Execute(base)
	if err != nil {
		t.Fatalf("Execute (MSL) failed: %v", err)
	}
	cdReq := base
	cdReq.Datum = "cd" // case-insensitive
	cdResp, err := uc.Execute(cdReq)
	if err != nil {
		t.Fatalf("Execute (CD) failed: %v", err)
	}

	if cdResp.Datum != datumCD {
		t.Errorf("datum = %q, want CD", cdResp.Datum)
	}
	offset := cdResp.ChartDatumOffsetM
	if offset <= 0 {
		t.Fatalf("expected a positive chart datum offset, got %v", offset)
	}
	// Every prediction shifts up by exactly the offset.
	for i := range mslResp.Predictions {
		diff := cdResp.Predictions[i].HeightM - mslResp.Predictions[i].HeightM
		if math.Abs(diff-offset) > 1e-6 {
			t.Fatalf("prediction[%d] shift = %v, want %v", i, diff, offset)
		}
	}
	// Extrema shift too.
	if len(mslResp.Extrema.Highs) == 0 || len(mslResp.Extrema.Lows) == 0 {
		t.Fatal("expected extrema to compare")
	}
	if diff := cdResp.Extrema.Highs[0].HeightM - mslResp.Extrema.Highs[0].HeightM; math.Abs(diff-offset) > 1e-6 {
		t.Errorf("high shift = %v, want %v", diff, offset)
	}
	if diff := cdResp.Extrema.Lows[0].HeightM - mslResp.Extrema.Lows[0].HeightM; math.Abs(diff-offset) > 1e-6 {
		t.Errorf("low shift = %v, want %v", diff, offset)
	}
}

// An unsupported datum value must be a validation error (mapped to 400).
func TestExecute_InvalidDatumIsValidationError(t *testing.T) {
	noAdjustmentEnv(t)

	loader := &mockConstituentLoader{constituents: openOceanConstituents()}
	uc := NewPredictionUseCase(loader, loader, nil)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := uc.Execute(PredictionRequest{
		Lat:      ptrFloat(38.0),
		Lon:      ptrFloat(144.0),
		Start:    start,
		End:      start.Add(time.Hour),
		Interval: 30 * time.Minute,
		Datum:    "LAT",
	})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation for unsupported datum, got %v", err)
	}
}

// An explicit datum_offset_m is the only thing that moves msl_m off 0; it still
// leaves the datum labelled MSL and is independent of chart_datum_offset_m.
func TestExecute_ExplicitDatumOffsetSetsMSL(t *testing.T) {
	noAdjustmentEnv(t)

	loader := &mockConstituentLoader{constituents: []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0, PhaseDeg: 0, SpeedDegPerHr: 28.9841042},
	}}
	uc := NewPredictionUseCase(loader, loader, nil)

	const offset = 0.768
	off := offset
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resp, err := uc.Execute(PredictionRequest{
		Lat:          ptrFloat(38.0),
		Lon:          ptrFloat(144.0),
		Start:        start,
		End:          start.Add(time.Hour),
		Interval:     30 * time.Minute,
		DatumOffsetM: &off,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if resp.MSL == nil || math.Abs(*resp.MSL-offset) > 1e-9 {
		t.Errorf("msl_m = %v, want %v", resp.MSL, offset)
	}
	// Zero-amplitude constituent: the whole height is the explicit offset.
	if got := resp.Predictions[0].HeightM; math.Abs(got-offset) > 1e-9 {
		t.Errorf("height = %v, want %v", got, offset)
	}
	if resp.Datum != datumMSL {
		t.Errorf("datum = %q, want MSL", resp.Datum)
	}
	if resp.Meta["datum_offset_m"] != "0.768" {
		t.Errorf("meta.datum_offset_m = %q, want %q", resp.Meta["datum_offset_m"], "0.768")
	}
}
