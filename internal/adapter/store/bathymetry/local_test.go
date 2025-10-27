package bathymetry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fhs/go-netcdf/netcdf"
)

// Helper to create a minimal GEBCO-like NetCDF file with the given elevation data.
func createElevationTestFile(t *testing.T, path string, latVals, lonVals []float64, values [][]float32) {
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
	velev, _ := f.AddVar("elevation", netcdf.FLOAT, []netcdf.Dim{latDim, lonDim})

	if err := f.EndDef(); err != nil {
		t.Fatalf("enddef: %v", err)
	}
	if err := vlat.WriteFloat64s(latVals); err != nil {
		t.Fatalf("write lat: %v", err)
	}
	if err := vlon.WriteFloat64s(lonVals); err != nil {
		t.Fatalf("write lon: %v", err)
	}
	flat := make([]float32, 0, len(latVals)*len(lonVals))
	for i := range values {
		flat = append(flat, values[i]...)
	}
	if err := velev.WriteFloat32s(flat); err != nil {
		t.Fatalf("write elevation: %v", err)
	}
}

func TestLocalStoreReloadsDepthGridForDistantLocations(t *testing.T) {
	latVals := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	lonVals := []float64{0, 1}
	values := make([][]float32, len(latVals))
	for i := range values {
		values[i] = make([]float32, len(lonVals))
		for j := range values[i] {
			values[i][j] = float32(-10*i - j - 1) // Negative depths (below sea level)
		}
	}
	dir := t.TempDir()
	gebcoPath := filepath.Join(dir, "gebco.nc")
	createElevationTestFile(t, gebcoPath, latVals, lonVals, values)

	store := NewLocalStore(gebcoPath, "", nil)

	metaNear, err := store.GetMetadata(1.0, 0.2)
	if err != nil {
		t.Fatalf("GetMetadata near: %v", err)
	}
	if metaNear == nil || metaNear.DepthM == nil {
		t.Fatalf("expected depth metadata near location, got %+v", metaNear)
	}
	depthNear := *metaNear.DepthM

	metaFar, err := store.GetMetadata(8.0, 0.2)
	if err != nil {
		t.Fatalf("GetMetadata far: %v", err)
	}
	if metaFar == nil || metaFar.DepthM == nil {
		t.Fatalf("expected depth metadata far location, got %+v", metaFar)
	}
	if depthNear == *metaFar.DepthM {
		t.Fatalf("expected different depth after reloading grid, got same value %.2f", depthNear)
	}
}

func TestLocalStoreHandlesWrappedLongitude(t *testing.T) {
	latVals := []float64{30, 31, 32}
	lonVals := []float64{230, 231, 232, 233}
	values := [][]float32{
		{-100, -101, -102, -103},
		{-110, -111, -112, -113},
		{-120, -121, -122, -123},
	}
	dir := t.TempDir()
	gebcoPath := filepath.Join(dir, "gebco_wrap.nc")
	createElevationTestFile(t, gebcoPath, latVals, lonVals, values)

	store := NewLocalStore(gebcoPath, "", nil)
	meta, err := store.GetMetadata(31.0, -130.0)
	if err != nil {
		t.Fatalf("GetMetadata wrapped lon: %v", err)
	}
	if meta == nil || meta.DepthM == nil {
		t.Fatalf("expected depth metadata for wrapped longitude, got %+v", meta)
	}
}
