package usecase

import (
	"fmt"
	"math"
	"testing"
	"time"

	"go.ngs.io/tides-api/internal/domain"
)

// mockConstituentLoader implements store.ConstituentLoader for tests.
type mockConstituentLoader struct {
	constituents []domain.ConstituentParam
	stationErr   error
	locationErr  error
}

func (m *mockConstituentLoader) LoadForStation(_ string) ([]domain.ConstituentParam, error) {
	if m.stationErr != nil {
		return nil, m.stationErr
	}
	return m.constituents, nil
}

func (m *mockConstituentLoader) LoadForLocation(_, _ float64) ([]domain.ConstituentParam, error) {
	if m.locationErr != nil {
		return nil, m.locationErr
	}
	return m.constituents, nil
}

// mockBathymetryStore implements bathymetry.Store for tests.
type mockBathymetryStore struct {
	meta *domain.LocationMetadata
}

func (m *mockBathymetryStore) GetMetadata(_, _ float64) (*domain.LocationMetadata, error) {
	return m.meta, nil
}

func (m *mockBathymetryStore) Close() error { return nil }

func ptrFloat(v float64) *float64 { return &v }

func ptrString(s string) *string { return &s }

// flatConstituents returns a single constituent with zero amplitude so that the
// predicted height is deterministic (exactly MSL) regardless of nodal corrections.
func flatConstituents() []domain.ConstituentParam {
	return []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0.0, PhaseDeg: 0.0, SpeedDegPerHr: 28.9841042},
	}
}

// Regression test for: depth_m double-counts MSL. CalculateTideHeight already includes
// MSL in HeightM (height starts at params.MSL), so water depth must be
// seabed_depth + HeightM, not seabed_depth + msl + HeightM.
func TestExecute_DepthDoesNotDoubleCountMSL(t *testing.T) {
	loader := &mockConstituentLoader{constituents: flatConstituents()}
	bathy := &mockBathymetryStore{
		meta: &domain.LocationMetadata{
			MSL:    1.0,
			DepthM: ptrFloat(10.0),
		},
	}
	uc := NewPredictionUseCase(loader, loader, bathy)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	req := PredictionRequest{
		Lat:      ptrFloat(0.0),
		Lon:      ptrFloat(-30.0), // Mid-Atlantic: far from any station override / datum offset.
		Start:    start,
		End:      start.Add(1 * time.Hour),
		Interval: 30 * time.Minute,
	}

	resp, err := uc.Execute(req)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(resp.Predictions) == 0 {
		t.Fatal("expected predictions, got none")
	}

	p := resp.Predictions[0]
	// With zero amplitude, HeightM == MSL == 1.0.
	if math.Abs(p.HeightM-1.0) > 1e-9 {
		t.Fatalf("precondition failed: expected HeightM 1.0 (== MSL), got %v", p.HeightM)
	}
	if p.DepthM == nil {
		t.Fatal("expected DepthM to be set")
	}

	// Correct: depth = seabed_depth + HeightM = 10.0 + 1.0 = 11.0 (MSL already in HeightM).
	if math.Abs(*p.DepthM-11.0) > 1e-9 {
		t.Errorf("depth_m double-counts MSL: expected 11.0 (seabed 10.0 + height 1.0), got %v", *p.DepthM)
	}
}

// Regression test for: roundToDecimal mis-rounds negative values because it uses
// int(val*1000+0.5), which truncates toward zero. It must behave like math.Round
// (half away from zero) at 3 decimal places.
func TestRoundToDecimal_NegativeValues(t *testing.T) {
	// roundToDecimal(-0.0006) must be -0.001, not 0.
	if got := roundToDecimal(-0.0006); math.Abs(got-(-0.001)) > 1e-12 {
		t.Errorf("roundToDecimal(-0.0006): expected -0.001, got %v", got)
	}

	// Inputs clearly on either side of the half-step have unambiguous results
	// (an exact .5 half-step is not representable in binary floating point, so
	// the boundary itself is not pinned).
	if got := roundToDecimal(-1.2346); math.Abs(got-(-1.235)) > 1e-12 {
		t.Errorf("roundToDecimal(-1.2346): expected -1.235, got %v", got)
	}
	if got := roundToDecimal(-1.2344); math.Abs(got-(-1.234)) > 1e-12 {
		t.Errorf("roundToDecimal(-1.2344): expected -1.234, got %v", got)
	}

	// Sanity check: positive rounding half up.
	if got := roundToDecimal(0.0006); math.Abs(got-0.001) > 1e-12 {
		t.Errorf("roundToDecimal(0.0006): expected 0.001, got %v", got)
	}
}

// Regression test for: a pointer to an empty StationID contradicts between Validate and
// Execute. Validate treats StationID pointing to "" as absent (so lat/lon validate OK),
// but Execute only checks StationID != nil and takes the station path, which then fails.
// The request must be processed via the lat/lon (FES) path.
func TestExecute_EmptyStationIDPointerUsesLatLonPath(t *testing.T) {
	csvLoader := &mockConstituentLoader{
		stationErr: fmt.Errorf("station path must not be used for empty station_id"),
	}
	fesLoader := &mockConstituentLoader{constituents: flatConstituents()}
	uc := NewPredictionUseCase(csvLoader, fesLoader, nil)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	req := PredictionRequest{
		StationID: ptrString(""),
		Lat:       ptrFloat(0.0),
		Lon:       ptrFloat(-30.0),
		Start:     start,
		End:       start.Add(1 * time.Hour),
		Interval:  30 * time.Minute,
	}

	// Precondition: Validate accepts this request as a lat/lon request.
	if err := req.Validate(); err != nil {
		t.Fatalf("precondition failed: Validate rejected the request: %v", err)
	}

	resp, err := uc.Execute(req)
	if err != nil {
		t.Fatalf("Execute failed for empty station_id with valid lat/lon: %v", err)
	}
	if resp.Source != "fes" {
		t.Errorf("expected lat/lon (fes) path, got source %q", resp.Source)
	}
}

// Regression test for: invalid timezone values are silently ignored and fall back to
// UTC. An unsupported timezone such as "pst" must produce an error instead of
// silently returning UTC-labelled timestamps.
func TestExecute_InvalidTimezoneReturnsError(t *testing.T) {
	loader := &mockConstituentLoader{constituents: flatConstituents()}
	uc := NewPredictionUseCase(loader, loader, nil)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	req := PredictionRequest{
		Lat:      ptrFloat(0.0),
		Lon:      ptrFloat(-30.0),
		Start:    start,
		End:      start.Add(1 * time.Hour),
		Interval: 30 * time.Minute,
		Timezone: "pst",
	}

	resp, err := uc.Execute(req)
	if err == nil {
		t.Errorf("expected error for unsupported timezone %q, got success (timezone label %q)", req.Timezone, resp.Timezone)
	}
}
