package geoid

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/fhs/go-netcdf/netcdf"
)

// createGeoidTestFile creates a minimal EGM2008-like NetCDF file with a "geoid" variable.
// values must have dimensions [len(latVals)][len(lonVals)].
func createGeoidTestFile(t *testing.T, path string, latVals, lonVals []float64, values [][]float64) {
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
	vgeoid, err := f.AddVar("geoid", netcdf.DOUBLE, []netcdf.Dim{latDim, lonDim})
	if err != nil {
		t.Fatalf("add geoid var: %v", err)
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
	flat := make([]float64, 0, len(latVals)*len(lonVals))
	for i := range values {
		flat = append(flat, values[i]...)
	}
	if err := vgeoid.WriteFloat64s(flat); err != nil {
		t.Fatalf("write geoid: %v", err)
	}
}

// Regression test for: the geoid grid subset is loaded once around the first
// queried location and never reloaded, so a later query outside that subset
// fails (or returns wrong values) instead of reloading the grid.
func TestGetGeoidHeight_ReloadsGridForDistantLocation(t *testing.T) {
	latVals := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	lonVals := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	// Planar function so bilinear interpolation is exact: N(lat, lon) = 10*lat + lon.
	values := make([][]float64, len(latVals))
	for i, la := range latVals {
		values[i] = make([]float64, len(lonVals))
		for j, lo := range lonVals {
			values[i][j] = 10*la + lo
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "egm2008.nc")
	createGeoidTestFile(t, path, latVals, lonVals, values)

	s := NewStore(path)

	// First query near (1, 1) loads a subset around that location.
	hNear, err := s.GetGeoidHeight(1.0, 1.0)
	if err != nil {
		t.Fatalf("GetGeoidHeight near: %v", err)
	}
	if math.Abs(hNear-11.0) > 1e-6 {
		t.Fatalf("expected geoid height 11.0 at (1,1), got %v", hNear)
	}

	// Second query at (9, 9) is far outside the first subset (+/-2 deg margin).
	hFar, err := s.GetGeoidHeight(9.0, 9.0)
	if err != nil {
		t.Fatalf("expected geoid grid to reload for distant location, got error: %v", err)
	}
	if math.Abs(hFar-99.0) > 1e-6 {
		t.Fatalf("expected geoid height 99.0 at (9,9), got %v", hFar)
	}
}

// Regression test for: negative (western hemisphere) longitudes are not
// normalized against a 0..360 longitude axis, so lookups like lon=-90 fail
// even though the grid covers the equivalent longitude 270.
func TestGetGeoidHeight_NormalizesNegativeLongitudeOn360Axis(t *testing.T) {
	latVals := []float64{29, 30, 31, 32, 33}
	// 0..360 longitude axis (5-degree steps).
	lonVals := make([]float64, 0, 73)
	for lo := 0.0; lo <= 360.0; lo += 5.0 {
		lonVals = append(lonVals, lo)
	}
	// N(lat, lon) = lon, so the expected value at lon=-90 (i.e. 270) is 270.
	values := make([][]float64, len(latVals))
	for i := range latVals {
		values[i] = make([]float64, len(lonVals))
		for j, lo := range lonVals {
			values[i][j] = lo
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "egm2008_360.nc")
	createGeoidTestFile(t, path, latVals, lonVals, values)

	s := NewStore(path)
	h, err := s.GetGeoidHeight(31.0, -90.0)
	if err != nil {
		t.Fatalf("expected lon=-90 to be normalized to 270 on 0..360 axis, got error: %v", err)
	}
	if math.Abs(h-270.0) > 1e-6 {
		t.Fatalf("expected geoid height 270.0 at lon=-90 (=270), got %v", h)
	}
}
