package bathymetry

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/fhs/go-netcdf/netcdf"
)

// createPackedElevationTestFile creates a GEBCO-like NetCDF file whose elevation
// variable is stored as packed int16 with scale_factor and add_offset attributes.
// True value = packed * scaleFactor + addOffset.
func createPackedElevationTestFile(t *testing.T, path string, latVals, lonVals []float64, packed [][]int16, scaleFactor, addOffset float64) {
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
	velev, err := f.AddVar("elevation", netcdf.SHORT, []netcdf.Dim{latDim, lonDim})
	if err != nil {
		t.Fatalf("add elevation var: %v", err)
	}
	if err := velev.Attr("scale_factor").WriteFloat64s([]float64{scaleFactor}); err != nil {
		t.Fatalf("write scale_factor: %v", err)
	}
	if err := velev.Attr("add_offset").WriteFloat64s([]float64{addOffset}); err != nil {
		t.Fatalf("write add_offset: %v", err)
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
	flat := make([]int16, 0, len(latVals)*len(lonVals))
	for i := range packed {
		flat = append(flat, packed[i]...)
	}
	if err := velev.WriteInt16s(flat); err != nil {
		t.Fatalf("write elevation: %v", err)
	}
}

// Regression test for: add_offset attribute is ignored when unpacking NetCDF data
// (only scale_factor is applied), so packed elevations decode to wrong values.
// With packed=100, scale_factor=0.1, add_offset=-50 the true elevation is
// 100*0.1 + (-50) = -40 m (depth 40 m); ignoring add_offset yields +10 m (no depth).
func TestLocalStoreAppliesAddOffset(t *testing.T) {
	latVals := []float64{30, 31, 32}
	lonVals := []float64{130, 131, 132}
	packed := [][]int16{
		{100, 100, 100},
		{100, 100, 100},
		{100, 100, 100},
	}
	dir := t.TempDir()
	gebcoPath := filepath.Join(dir, "gebco_packed.nc")
	createPackedElevationTestFile(t, gebcoPath, latVals, lonVals, packed, 0.1, -50.0)

	store := NewLocalStore(gebcoPath, "", nil)
	meta, err := store.GetMetadata(31.0, 131.0)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if meta == nil || meta.DepthM == nil {
		t.Fatalf("expected depth metadata (elevation -40 m after add_offset), got %+v", meta)
	}
	if math.Abs(*meta.DepthM-40.0) > 0.5 {
		t.Fatalf("expected depth ~40 m (packed*scale_factor + add_offset), got %v", *meta.DepthM)
	}
}

// Regression test for: near the 0/360 longitude seam of a wrapped axis, the
// +/-2 degree subset window is computed from wrapped endpoint indices on
// opposite ends of the axis, producing a subset that does not actually contain
// the target longitude, so interpolation fails and no depth is returned.
func TestLocalStoreDepthNearLongitudeSeam(t *testing.T) {
	latVals := []float64{30, 31, 32}
	// Full 0..359 longitude axis (1-degree steps) - a wrapped axis.
	lonVals := make([]float64, 360)
	for i := range lonVals {
		lonVals[i] = float64(i)
	}
	values := make([][]float32, len(latVals))
	for i := range values {
		values[i] = make([]float32, len(lonVals))
		for j := range values[i] {
			values[i][j] = -100 // Uniform 100 m depth.
		}
	}
	dir := t.TempDir()
	gebcoPath := filepath.Join(dir, "gebco_seam.nc")
	createElevationTestFile(t, gebcoPath, latVals, lonVals, values)

	for name, lon := range map[string]float64{
		"east_of_seam": 0.5,  // Just east of the seam.
		"west_of_seam": -0.5, // Just west of the seam (wraps to 359.5).
	} {
		t.Run(name, func(t *testing.T) {
			store := NewLocalStore(gebcoPath, "", nil)
			meta, err := store.GetMetadata(31.0, lon)
			if err != nil {
				t.Fatalf("GetMetadata(31, %v): %v", lon, err)
			}
			if meta == nil || meta.DepthM == nil {
				t.Fatalf("expected depth metadata near longitude seam (lon=%v), got %+v", lon, meta)
			}
			if math.Abs(*meta.DepthM-100.0) > 0.5 {
				t.Fatalf("expected depth ~100 m near seam (lon=%v), got %v", lon, *meta.DepthM)
			}
		})
	}
}
