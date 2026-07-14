package usecase

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.ngs.io/tides-api/internal/adapter/store"
	"go.ngs.io/tides-api/internal/domain"
)

// GetParameters for a lat/lon query must return the FES constituents, the
// bathymetry MSL, the seabed depth, and the FES phase reference epoch, with
// the equilibrium argument evaluated at that epoch.
func TestGetParameters_LatLonReturnsModelParameters(t *testing.T) {
	resetAdjustmentTables(t)

	loader := &mockConstituentLoader{constituents: []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0.51, PhaseDeg: 133.1, SpeedDegPerHr: 28.9841042},
	}}
	bathy := &mockBathymetryStore{meta: &domain.LocationMetadata{
		MSL:        0.38,
		DepthM:     ptrFloat(2.64),
		DatumName:  "EGM2008",
		SourceName: "DTU21 MSS",
	}}
	uc := NewPredictionUseCase(loader, loader, bathy)

	// Mid-Atlantic: far from any station override / datum offset.
	resp, err := uc.GetParameters(ParametersRequest{Lat: ptrFloat(0.0), Lon: ptrFloat(-30.0)})
	if err != nil {
		t.Fatalf("GetParameters failed: %v", err)
	}

	if resp.Source != sourceFES {
		t.Errorf("source = %q, want %q", resp.Source, sourceFES)
	}
	if resp.Datum != datumMSL {
		t.Errorf("datum = %q, want %q", resp.Datum, datumMSL)
	}
	if resp.Location == nil {
		t.Fatal("location must be set for lat/lon queries")
	}
	if resp.Location.Lat != 0.0 || resp.Location.Lon != -30.0 {
		t.Errorf("location = %+v, want {0 -30}", *resp.Location)
	}
	if resp.StationID != nil {
		t.Errorf("station_id must be omitted for lat/lon queries, got %q", *resp.StationID)
	}
	// msl_m is 0: the constants are MSL-referenced with zero mean. The model
	// MSL (mean dynamic topography) is reported only as meta.mdt_m.
	if resp.MSL != 0.0 {
		t.Errorf("msl_m = %v, want 0", resp.MSL)
	}
	if resp.Meta["mdt_m"] != "0.380" {
		t.Errorf("meta.mdt_m = %q, want %q", resp.Meta["mdt_m"], "0.380")
	}
	// No override or auto offset here, so the chart datum offset falls back to
	// the Σ(H_M2+H_S2+H_K1+H_O1) proxy; only M2 (0.51) is present.
	if math.Abs(resp.ChartDatumOffsetM-0.51) > 1e-9 {
		t.Errorf("chart_datum_offset_m = %v, want 0.51", resp.ChartDatumOffsetM)
	}
	if resp.SeabedDepth == nil || math.Abs(*resp.SeabedDepth-2.64) > 1e-9 {
		t.Errorf("seabed_depth_m = %v, want 2.64", resp.SeabedDepth)
	}

	// FES phases are referenced to the FES epoch (2012-01-01T00:00:00Z).
	wantRef := time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)
	if resp.ReferenceTime != wantRef.Format(time.RFC3339) {
		t.Errorf("reference_time = %q, want %q", resp.ReferenceTime, wantRef.Format(time.RFC3339))
	}

	if len(resp.Constituents) != 1 {
		t.Fatalf("expected 1 constituent, got %d", len(resp.Constituents))
	}
	c := resp.Constituents[0]
	if c.Name != "M2" {
		t.Errorf("constituent name = %q, want M2", c.Name)
	}
	if math.Abs(c.SpeedDegPerHr-28.9841042) > 1e-9 {
		t.Errorf("speed_deg_per_hr = %v, want 28.9841042", c.SpeedDegPerHr)
	}
	if math.Abs(c.AmplitudeM-0.51) > 1e-9 {
		t.Errorf("amplitude_m = %v, want 0.51", c.AmplitudeM)
	}
	if math.Abs(c.PhaseDeg-133.1) > 1e-9 {
		t.Errorf("phase_deg = %v, want 133.1", c.PhaseDeg)
	}

	// V must be the Greenwich equilibrium argument evaluated at the absolute
	// reference epoch (hours since Unix epoch), matching what the server-side
	// prediction uses.
	refHours := wantRef.Sub(time.Unix(0, 0).UTC()).Hours()
	wantV := domain.NewAstronomicalNodalCorrection().GetEquilibriumArgument("M2", refHours)
	if math.Abs(c.EquilibriumArgumentDeg-wantV) > 1e-9 {
		t.Errorf("equilibrium_argument_deg = %v, want %v", c.EquilibriumArgumentDeg, wantV)
	}

	if resp.Meta["attribution"] == "" {
		t.Error("meta.attribution must be set")
	}
	if resp.Meta["datum_name"] != "EGM2008" {
		t.Errorf("meta.datum_name = %q, want EGM2008", resp.Meta["datum_name"])
	}
	if resp.Meta["metadata_source"] != "DTU21 MSS" {
		t.Errorf("meta.metadata_source = %q, want %q", resp.Meta["metadata_source"], "DTU21 MSS")
	}
}

// GetParameters for a station query must use the CSV store, echo the station
// ID, use the Unix epoch as reference time, and omit location/bathymetry.
func TestGetParameters_StationUsesCSVPath(t *testing.T) {
	resetAdjustmentTables(t)

	loader := &mockConstituentLoader{constituents: []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0.5, PhaseDeg: 30.0, SpeedDegPerHr: 28.9841042},
	}}
	uc := NewPredictionUseCase(loader, loader, nil)

	resp, err := uc.GetParameters(ParametersRequest{StationID: ptrString("tokyo")})
	if err != nil {
		t.Fatalf("GetParameters failed: %v", err)
	}

	if resp.Source != sourceCSV {
		t.Errorf("source = %q, want %q", resp.Source, sourceCSV)
	}
	if resp.StationID == nil || *resp.StationID != "tokyo" {
		t.Errorf("station_id = %v, want tokyo", resp.StationID)
	}
	if resp.Location != nil {
		t.Errorf("location must be omitted for station queries, got %+v", *resp.Location)
	}
	if resp.SeabedDepth != nil {
		t.Errorf("seabed_depth_m must be omitted without bathymetry, got %v", *resp.SeabedDepth)
	}
	if resp.MSL != 0.0 {
		t.Errorf("msl_m = %v, want 0 (no bathymetry)", resp.MSL)
	}

	// CSV phases are referenced to the Unix epoch.
	wantRef := time.Unix(0, 0).UTC()
	if resp.ReferenceTime != wantRef.Format(time.RFC3339) {
		t.Errorf("reference_time = %q, want %q", resp.ReferenceTime, wantRef.Format(time.RFC3339))
	}

	if len(resp.Constituents) != 1 {
		t.Fatalf("expected 1 constituent, got %d", len(resp.Constituents))
	}
	wantV := domain.NewAstronomicalNodalCorrection().GetEquilibriumArgument("M2", 0)
	if math.Abs(resp.Constituents[0].EquilibriumArgumentDeg-wantV) > 1e-9 {
		t.Errorf("equilibrium_argument_deg = %v, want %v", resp.Constituents[0].EquilibriumArgumentDeg, wantV)
	}
}

// A matching station override must surface its fitted intercept as the chart
// datum offset (msl_m stays 0) and substitute the fitted constituents, exactly
// as Execute does.
func TestGetParameters_OverrideSuppliesChartDatumOffsetAndConstituents(t *testing.T) {
	resetAdjustmentTables(t)

	const (
		lat       = 35.38153
		lon       = 139.867951
		modelMSL  = 0.38
		intercept = 1.15
	)

	dir := t.TempDir()
	overridesPath := filepath.Join(dir, "overrides.json")
	writeJSON(t, overridesPath, []map[string]any{
		{
			fieldName: "KZ", fieldStation: "KZ", fieldLat: lat, fieldLon: lon,
			fieldRadius: 40, fieldDatumOffset: intercept,
			fieldConstituents: []map[string]any{
				{fieldName: "M2", fieldAmplitude: 0.51, fieldPhase: 133.1},
			},
		},
	})
	t.Setenv("STATION_OVERRIDES_PATH", overridesPath)
	t.Setenv("DATUM_OFFSETS_PATH", filepath.Join(dir, "nonexistent.json"))

	loader := &mockConstituentLoader{constituents: []domain.ConstituentParam{
		{Name: "M2", AmplitudeM: 0.2, PhaseDeg: 10.0, SpeedDegPerHr: 28.9841042},
	}}
	bathy := &mockBathymetryStore{meta: &domain.LocationMetadata{MSL: modelMSL}}
	uc := NewPredictionUseCase(loader, loader, bathy)

	resp, err := uc.GetParameters(ParametersRequest{Lat: ptrFloat(lat), Lon: ptrFloat(lon)})
	if err != nil {
		t.Fatalf("GetParameters failed: %v", err)
	}

	// Heights are MSL-centred (msl_m = 0); the override intercept becomes the
	// chart datum offset and the model MSL is excluded from both.
	if resp.MSL != 0.0 {
		t.Errorf("msl_m = %v, want 0", resp.MSL)
	}
	if math.Abs(resp.ChartDatumOffsetM-intercept) > 1e-9 {
		t.Errorf("chart_datum_offset_m = %v, want %v (override intercept)", resp.ChartDatumOffsetM, intercept)
	}

	if len(resp.Constituents) != 1 {
		t.Fatalf("expected 1 constituent, got %d", len(resp.Constituents))
	}
	c := resp.Constituents[0]
	if math.Abs(c.AmplitudeM-0.51) > 1e-9 || math.Abs(c.PhaseDeg-133.1) > 1e-9 {
		t.Errorf("constituent = %+v, want override amplitude 0.51 / phase 133.1", c)
	}
}

// GetParameters must apply the same validation rules as predictions for the
// location and source parameters.
func TestGetParameters_Validation(t *testing.T) {
	loader := &mockConstituentLoader{constituents: flatConstituents()}
	uc := NewPredictionUseCase(loader, loader, nil)

	cases := []struct {
		name string
		req  ParametersRequest
	}{
		{"no location", ParametersRequest{}},
		{"both lat/lon and station_id", ParametersRequest{
			Lat: ptrFloat(35.0), Lon: ptrFloat(139.0), StationID: ptrString("tokyo"),
		}},
		{"latitude out of range", ParametersRequest{Lat: ptrFloat(91.0), Lon: ptrFloat(0.0)}},
		{"longitude out of range", ParametersRequest{Lat: ptrFloat(0.0), Lon: ptrFloat(181.0)}},
		// Non-finite coordinates must be rejected: every comparison against
		// NaN is false, so a bare range check would let them through.
		{"NaN latitude", ParametersRequest{Lat: ptrFloat(math.NaN()), Lon: ptrFloat(0.0)}},
		{"NaN longitude", ParametersRequest{Lat: ptrFloat(0.0), Lon: ptrFloat(math.NaN())}},
		{"Inf latitude", ParametersRequest{Lat: ptrFloat(math.Inf(1)), Lon: ptrFloat(0.0)}},
		{"negative Inf longitude", ParametersRequest{Lat: ptrFloat(0.0), Lon: ptrFloat(math.Inf(-1))}},
		{"station_id with fes source", ParametersRequest{
			StationID: ptrString("tokyo"), Source: "fes",
		}},
		{"lat/lon with csv source", ParametersRequest{
			Lat: ptrFloat(0.0), Lon: ptrFloat(-30.0), Source: "csv",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := uc.GetParameters(tc.req)
			if !errors.Is(err, ErrValidation) {
				t.Errorf("expected ErrValidation, got %v", err)
			}
		})
	}
}

// An unknown station (missing data file) must map to ErrNotFound so the
// handler can return 404, matching Execute.
func TestGetParameters_UnknownStationIsNotFound(t *testing.T) {
	loader := &mockConstituentLoader{
		stationErr: fmt.Errorf("open failed: %w", fs.ErrNotExist),
	}
	uc := NewPredictionUseCase(loader, loader, nil)

	_, err := uc.GetParameters(ParametersRequest{StationID: ptrString("nosuchstation")})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// An inland coordinate has no FES data (every grid neighbour is a fill value):
// the store reports store.ErrNoData, which must surface as ErrNotFound with a
// human-readable message rather than as an internal error.
func TestGetParameters_LandLocationIsNotFound(t *testing.T) {
	resetAdjustmentTables(t)

	loader := &mockConstituentLoader{
		locationErr: fmt.Errorf("%w: no valid constituents at (36.2572, 139.3759)", store.ErrNoData),
	}
	uc := NewPredictionUseCase(loader, loader, nil)

	// Gunma prefecture: inland, far from any FES water cell.
	_, err := uc.GetParameters(ParametersRequest{Lat: ptrFloat(36.2572), Lon: ptrFloat(139.3759)})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	want := "not found: no tide data at (36.2572, 139.3759) - the location may be on land"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// The prediction path shares resolvePredictionParams, so an inland coordinate
// must be a 404 there too.
func TestExecute_LandLocationIsNotFound(t *testing.T) {
	resetAdjustmentTables(t)

	loader := &mockConstituentLoader{
		locationErr: fmt.Errorf("%w: no valid constituents at (36.2572, 139.3759)", store.ErrNoData),
	}
	uc := NewPredictionUseCase(loader, loader, nil)

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := uc.Execute(PredictionRequest{
		Lat:      ptrFloat(36.2572),
		Lon:      ptrFloat(139.3759),
		Start:    start,
		End:      start.Add(24 * time.Hour),
		Interval: 10 * time.Minute,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "may be on land") {
		t.Errorf("error must explain the cause to the client, got %q", err.Error())
	}
}

// A genuine internal failure (e.g. an unreadable NetCDF file) must NOT be
// downgraded to a 404.
func TestExecute_LocationLoadFailureIsInternal(t *testing.T) {
	resetAdjustmentTables(t)

	loader := &mockConstituentLoader{locationErr: errors.New("read grid: i/o error")}
	uc := NewPredictionUseCase(loader, loader, nil)

	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := uc.Execute(PredictionRequest{
		Lat:      ptrFloat(36.2572),
		Lon:      ptrFloat(139.3759),
		Start:    start,
		End:      start.Add(24 * time.Hour),
		Interval: 10 * time.Minute,
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrValidation) {
		t.Errorf("internal failure must not map to a typed client error, got %v", err)
	}
}
