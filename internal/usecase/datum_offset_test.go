package usecase

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.ngs.io/tides-api/internal/domain"
)

// JSON fixture key names shared across tests.
const (
	fieldLat = "lat"
	fieldLon = "lon"
)

// resetAdjustmentTables clears the lazily-loaded datum offset and station
// override tables so a test can load its own fixtures, and restores the
// pristine state afterwards so other tests reload from their own environment.
func resetAdjustmentTables(t *testing.T) {
	t.Helper()
	reset := func() {
		datumOnce = sync.Once{}
		datumTable = nil
		overridesOnce = sync.Once{}
		overridesTable = nil
	}
	reset()
	t.Cleanup(reset)
}

// Regression test for: when a location matches both a JMA datum offset entry
// (data/jma_datum_offsets.json, applied via getAutoDatumOffset) and a station
// override with datum_offset_m (data/jma_station_overrides.json, applied as
// the base MSL term in Execute), the same fitted offset was added twice.
// Predictions near every JMA station were biased by a full extra datum offset
// (~1m scale). The offset must be applied exactly once.
func TestExecute_DatumOffsetNotDoubleCountedWithStationOverride(t *testing.T) {
	resetAdjustmentTables(t)

	const (
		lat       = 35.38153
		lon       = 139.867951
		offset    = 1.0
		fieldName = "name"
	)

	dir := t.TempDir()
	datumPath := filepath.Join(dir, "datum.json")
	writeJSON(t, datumPath, []map[string]any{
		{fieldName: "KZ", fieldLat: lat, fieldLon: lon, "offset_m": offset},
	})
	overridesPath := filepath.Join(dir, "overrides.json")
	writeJSON(t, overridesPath, []map[string]any{
		{
			fieldName: "KZ", "station": "KZ", fieldLat: lat, fieldLon: lon,
			"radius_km": 40, "datum_offset_m": offset,
			"constituents": []map[string]any{
				{fieldName: "M2", "amplitude_m": 0.0, "phase_deg": 0.0},
			},
		},
	})
	t.Setenv("DATUM_OFFSETS_PATH", datumPath)
	t.Setenv("STATION_OVERRIDES_PATH", overridesPath)

	// Zero-amplitude constituents make every predicted height equal to the MSL
	// term, which isolates the applied datum offset.
	loader := &mockConstituentLoader{constituents: []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0, PhaseDeg: 0, SpeedDegPerHr: 28.9841042},
	}}
	uc := NewPredictionUseCase(loader, loader, nil)

	reqLat, reqLon := lat, lon
	resp, err := uc.Execute(PredictionRequest{
		Lat:      &reqLat,
		Lon:      &reqLon,
		Start:    time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2025, 6, 1, 2, 0, 0, 0, time.UTC),
		Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(resp.Predictions) == 0 {
		t.Fatal("no predictions returned")
	}
	got := resp.Predictions[0].HeightM
	if got != offset {
		t.Errorf("datum offset applied %.1f times: height = %.3f, want %.3f (offset applied exactly once)",
			got/offset, got, offset)
	}
}

// Regression test for: when bathymetry provides a model MSL (DTU21 MSS,
// geoid-corrected) and a station override with datum_offset_m matches, the
// override's fitted intercept was ADDED to the model MSL. The intercept is the
// full constant term relative to the JMA datum (mean sea level above DL), so
// stacking the model MSL on top shifts every height by metadata.MSL
// (~0.38 m at Kisarazu in production). The override intercept must REPLACE the
// model MSL, not add to it.
func TestExecute_OverrideDatumReplacesModelMSL(t *testing.T) {
	resetAdjustmentTables(t)

	const (
		lat       = 35.38153
		lon       = 139.867951
		modelMSL  = 0.38 // DTU21 MSS geoid-corrected sea surface height.
		intercept = 1.15 // jma-harmonics fitted constant (MSL above DL).
	)

	dir := t.TempDir()
	overridesPath := filepath.Join(dir, "overrides.json")
	writeJSON(t, overridesPath, []map[string]any{
		{
			"name": "KZ", "station": "KZ", fieldLat: lat, fieldLon: lon,
			"radius_km": 40, "datum_offset_m": intercept,
			"constituents": []map[string]any{
				{"name": "M2", "amplitude_m": 0.0, "phase_deg": 0.0},
			},
		},
	})
	t.Setenv("STATION_OVERRIDES_PATH", overridesPath)
	t.Setenv("DATUM_OFFSETS_PATH", filepath.Join(dir, "nonexistent.json"))

	loader := &mockConstituentLoader{constituents: []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0, PhaseDeg: 0, SpeedDegPerHr: 28.9841042},
	}}
	bathy := &mockBathymetryStore{meta: &domain.LocationMetadata{MSL: modelMSL}}
	uc := NewPredictionUseCase(loader, loader, bathy)

	reqLat, reqLon := lat, lon
	resp, err := uc.Execute(PredictionRequest{
		Lat:      &reqLat,
		Lon:      &reqLon,
		Start:    time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2026, 7, 13, 2, 0, 0, 0, time.UTC),
		Interval: time.Hour,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(resp.Predictions) == 0 {
		t.Fatal("no predictions returned")
	}
	got := resp.Predictions[0].HeightM
	if got != intercept {
		t.Errorf("height = %.3f, want %.3f (override intercept must replace the model MSL %.2f, not stack on it)",
			got, intercept, modelMSL)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", strings.TrimPrefix(path, os.TempDir()), err)
	}
}
