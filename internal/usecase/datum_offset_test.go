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
	fieldLat          = "lat"
	fieldLon          = "lon"
	fieldName         = "name"
	fieldStation      = "station"
	fieldDatumOffset  = "datum_offset_m"
	fieldRadius       = "radius_km"
	fieldConstituents = "constituents"
	fieldAmplitude    = "amplitude_m"
	fieldPhase        = "phase_deg"
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
// override with datum_offset_m (data/jma_station_overrides.json), the same
// fitted offset must resolve to the chart datum offset exactly once - not
// doubled. Under the MSL-centred contract the offset never shifts heights
// (default datum=MSL stays 0-centred); it surfaces as chart_datum_offset_m and,
// with datum=CD, as the amount added to every height.
func TestExecute_ChartDatumOffsetNotDoubleCountedWithStationOverride(t *testing.T) {
	resetAdjustmentTables(t)

	const (
		lat    = 35.38153
		lon    = 139.867951
		offset = 1.0
	)

	dir := t.TempDir()
	datumPath := filepath.Join(dir, "datum.json")
	writeJSON(t, datumPath, []map[string]any{
		{fieldName: "KZ", fieldLat: lat, fieldLon: lon, "offset_m": offset},
	})
	overridesPath := filepath.Join(dir, "overrides.json")
	writeJSON(t, overridesPath, []map[string]any{
		{
			fieldName: "KZ", fieldStation: "KZ", fieldLat: lat, fieldLon: lon,
			fieldRadius: 40, fieldDatumOffset: offset,
			fieldConstituents: []map[string]any{
				{fieldName: "M2", fieldAmplitude: 0.0, fieldPhase: 0.0},
			},
		},
	})
	t.Setenv("DATUM_OFFSETS_PATH", datumPath)
	t.Setenv("STATION_OVERRIDES_PATH", overridesPath)

	// Zero-amplitude constituents make every MSL-referenced height exactly 0,
	// which isolates the chart datum offset applied under datum=CD.
	loader := &mockConstituentLoader{constituents: []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0, PhaseDeg: 0, SpeedDegPerHr: 28.9841042},
	}}
	uc := NewPredictionUseCase(loader, loader, nil)

	reqLat, reqLon := lat, lon
	base := PredictionRequest{
		Lat:      &reqLat,
		Lon:      &reqLon,
		Start:    time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
		End:      time.Date(2025, 6, 1, 2, 0, 0, 0, time.UTC),
		Interval: time.Hour,
	}

	resp, err := uc.Execute(base)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(resp.Predictions) == 0 {
		t.Fatal("no predictions returned")
	}
	if got := resp.ChartDatumOffsetM; got != offset {
		t.Errorf("chart_datum_offset_m applied %.1f times: got %.3f, want %.3f (resolved exactly once)",
			got/offset, got, offset)
	}
	if got := resp.Predictions[0].HeightM; got != 0 {
		t.Errorf("datum=MSL height = %.3f, want 0 (heights are MSL-centred)", got)
	}

	cdReq := base
	cdReq.Datum = "CD"
	cdResp, err := uc.Execute(cdReq)
	if err != nil {
		t.Fatalf("Execute (datum=CD) failed: %v", err)
	}
	if got := cdResp.Predictions[0].HeightM; got != offset {
		t.Errorf("datum=CD height = %.3f, want %.3f (offset added exactly once)", got, offset)
	}
}

// Regression test for: when bathymetry provides a model MSL / mean dynamic
// topography (DTU21 MSS − EGM2008) and a station override with datum_offset_m
// matches, the MDT must NOT be mixed into heights, and the override intercept
// (mean sea level above DL) must become the chart datum offset. Heights stay
// MSL-centred; the MDT is reported only as meta.mdt_m.
func TestExecute_ChartDatumFromOverrideExcludesMDT(t *testing.T) {
	resetAdjustmentTables(t)

	const (
		lat       = 35.38153
		lon       = 139.867951
		modelMSL  = 0.38 // DTU21 MSS geoid-corrected sea surface height (MDT).
		intercept = 1.15 // jma-harmonics fitted constant (MSL above DL).
	)

	dir := t.TempDir()
	overridesPath := filepath.Join(dir, "overrides.json")
	writeJSON(t, overridesPath, []map[string]any{
		{
			fieldName: "KZ", fieldStation: "KZ", fieldLat: lat, fieldLon: lon,
			fieldRadius: 40, fieldDatumOffset: intercept,
			fieldConstituents: []map[string]any{
				{fieldName: "M2", fieldAmplitude: 0.0, fieldPhase: 0.0},
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
	if got := resp.Predictions[0].HeightM; got != 0 {
		t.Errorf("height = %.3f, want 0 (MDT %.2f must not shift heights)", got, modelMSL)
	}
	if got := resp.ChartDatumOffsetM; got != intercept {
		t.Errorf("chart_datum_offset_m = %.3f, want %.3f (override intercept)", got, intercept)
	}
	if got := resp.Meta["mdt_m"]; got != "0.380" {
		t.Errorf("meta.mdt_m = %q, want %q", got, "0.380")
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
