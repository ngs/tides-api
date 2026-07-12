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
// override with datum_offset_m (data/jma_station_overrides.json, applied via
// applyStationOverride), the same fitted offset was added twice. Predictions
// near every JMA station were biased by a full extra datum offset (~1m scale).
// The offset must be applied exactly once.
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
		{fieldName: "KZ", "lat": lat, "lon": lon, "offset_m": offset},
	})
	overridesPath := filepath.Join(dir, "overrides.json")
	writeJSON(t, overridesPath, []map[string]any{
		{
			fieldName: "KZ", "station": "KZ", "lat": lat, "lon": lon,
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
