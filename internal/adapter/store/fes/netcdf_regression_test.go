package fes

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/fhs/go-netcdf/netcdf"
)

// createGridAmpPhaseNC creates a combined NetCDF with lat, lon, amplitude, phase
// using arbitrary grid coordinates and sizes.
// If ampFill is non-nil, a _FillValue attribute is set on the amplitude variable.
func createGridAmpPhaseNC(t *testing.T, path string, latVals, lonVals []float64, amp, phase [][]float32, ampFill *float32) {
	t.Helper()
	//nolint:gosec // G301: Standard test directory permissions.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := netcdf.CreateFile(path, netcdf.CLOBBER)
	if err != nil {
		t.Fatalf("create nc: %v", err)
	}
	defer func() { _ = f.Close() }()

	latDim, _ := f.AddDim("lat", uint64(len(latVals)))
	lonDim, _ := f.AddDim("lon", uint64(len(lonVals)))
	vlat, _ := f.AddVar("lat", netcdf.DOUBLE, []netcdf.Dim{latDim})
	vlon, _ := f.AddVar("lon", netcdf.DOUBLE, []netcdf.Dim{lonDim})
	vAmp, err := f.AddVar("amplitude", netcdf.FLOAT, []netcdf.Dim{latDim, lonDim})
	if err != nil {
		t.Fatalf("add amplitude: %v", err)
	}
	vPhase, err := f.AddVar("phase", netcdf.FLOAT, []netcdf.Dim{latDim, lonDim})
	if err != nil {
		t.Fatalf("add phase: %v", err)
	}
	if ampFill != nil {
		if err := vAmp.Attr("_FillValue").WriteFloat32s([]float32{*ampFill}); err != nil {
			t.Fatalf("write _FillValue: %v", err)
		}
	}

	if err := f.EndDef(); err != nil {
		t.Fatalf("enddef: %v", err)
	}

	if err := vlat.WriteFloat64s(latVals); err != nil {
		t.Fatalf("write lat: %v", err)
	}
	if err := vlon.WriteFloat64s(lonVals); err != nil {
		t.Fatalf("write lon: %v", err)
	}
	flatten := func(values [][]float32) []float32 {
		flat := make([]float32, 0, len(latVals)*len(lonVals))
		for i := range values {
			flat = append(flat, values[i]...)
		}
		return flat
	}
	if err := vAmp.WriteFloat32s(flatten(amp)); err != nil {
		t.Fatalf("write amplitude: %v", err)
	}
	if err := vPhase.WriteFloat32s(flatten(phase)); err != nil {
		t.Fatalf("write phase: %v", err)
	}
}

// Regression test for: cm->m conversion is applied twice for ocean_tide files
// (once inside interpolatePointFromNetCDF, once in interpolateConstituentAtPoint),
// so an amplitude stored as 100 cm comes back as 0.01 m instead of 1.0 m.
func TestLoadForLocation_OceanTideCmToM_AppliedOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ocean_tide", "m2.nc")
	// Amplitude stored in cm: 100 cm everywhere -> expected 1.0 m.
	createGridAmpPhaseNC(t, path,
		[]float64{35.0, 36.0},
		[]float64{139.0, 140.0},
		[][]float32{{100, 100}, {100, 100}},
		[][]float32{{10, 10}, {10, 10}},
		nil,
	)

	s := NewStore(dir)
	params, err := s.LoadForLocation(35.5, 139.5)
	if err != nil {
		t.Fatalf("LoadForLocation: %v", err)
	}

	var found bool
	for _, p := range params {
		if p.Name == "M2" {
			found = true
			if p.AmplitudeM < 0.95 || p.AmplitudeM > 1.05 {
				t.Fatalf("expected amplitude ~1.0 m (100 cm converted once), got %v m", p.AmplitudeM)
			}
		}
	}
	if !found {
		t.Fatalf("M2 constituent not found in params: %+v", params)
	}
}

// Regression test for: longitudes just west of the prime meridian (e.g. -0.01 -> 359.99)
// fall between the last grid column and 360 on a 0..360 longitude axis, and the grid
// cell lookup fails instead of wrapping around to the first column.
func TestLoadForLocation_WrapAroundNearPrimeMeridian(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m2.nc")
	// 0..360 style longitude axis where the last grid point is < 360.
	createGridAmpPhaseNC(t, path,
		[]float64{35.0, 36.0},
		[]float64{0.0, 120.0, 240.0, 359.9375},
		[][]float32{{100, 100, 100, 100}, {100, 100, 100, 100}},
		[][]float32{{10, 10, 10, 10}, {10, 10, 10, 10}},
		nil,
	)

	s := NewStore(dir)
	// -0.01 normalizes to 359.99, which lies between 359.9375 and 360 (wrap cell).
	params, err := s.LoadForLocation(35.5, -0.01)
	if err != nil {
		t.Fatalf("expected LoadForLocation to succeed via wrap-around interpolation, got error: %v", err)
	}
	if len(params) == 0 {
		t.Fatalf("expected at least one constituent")
	}
	for _, p := range params {
		if p.Name == "M2" {
			if p.AmplitudeM <= 0 {
				t.Fatalf("expected positive amplitude, got %+v", p)
			}
			return
		}
	}
	t.Fatalf("M2 constituent not found in params: %+v", params)
}

// Regression test for: phase is linearly interpolated across the 0/360 discontinuity,
// so neighboring grid points with phases 359 deg and 1 deg interpolate to ~180 deg
// instead of ~0 deg.
func TestLoadForLocation_PhaseWrapAcrossZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m2.nc")
	createGridAmpPhaseNC(t, path,
		[]float64{35.0, 36.0},
		[]float64{139.0, 140.0},
		[][]float32{{100, 100}, {100, 100}},
		// West column phase 359 deg, east column phase 1 deg: midpoint should be ~0 deg.
		[][]float32{{359, 1}, {359, 1}},
		nil,
	)

	s := NewStore(dir)
	params, err := s.LoadForLocation(35.5, 139.5)
	if err != nil {
		t.Fatalf("LoadForLocation: %v", err)
	}
	for _, p := range params {
		if p.Name == "M2" {
			// Angular distance from 0 degrees must be small.
			d := math.Mod(math.Abs(p.PhaseDeg), 360.0)
			if d > 180 {
				d = 360 - d
			}
			if d > 5.0 {
				t.Fatalf("expected interpolated phase ~0 deg (circular interpolation of 359 and 1), got %v deg", p.PhaseDeg)
			}
			return
		}
	}
	t.Fatalf("M2 constituent not found in params: %+v", params)
}

// Regression test for: _FillValue grid points (land) are replaced with 0 and included
// in the bilinear interpolation, underestimating amplitude in coastal cells.
// With 3 valid points of 1.0 m and one land point, the result should be ~1.0 m, not 0.75 m.
func TestLoadForLocation_FillValueExcludedFromInterpolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m2.nc")
	fill := float32(-9999)
	createGridAmpPhaseNC(t, path,
		[]float64{35.0, 36.0},
		[]float64{139.0, 140.0},
		// 3 valid points at 100 (interpreted as 1.0 m after cm->m), one land point.
		[][]float32{{100, 100}, {100, -9999}},
		[][]float32{{10, 10}, {10, 10}},
		&fill,
	)

	s := NewStore(dir)
	params, err := s.LoadForLocation(35.5, 139.5)
	if err != nil {
		t.Fatalf("LoadForLocation: %v", err)
	}
	for _, p := range params {
		if p.Name == "M2" {
			if p.AmplitudeM < 0.95 || p.AmplitudeM > 1.05 {
				t.Fatalf("expected amplitude ~1.0 m with fill point excluded, got %v m", p.AmplitudeM)
			}
			return
		}
	}
	t.Fatalf("M2 constituent not found in params: %+v", params)
}
