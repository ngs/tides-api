package fes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fhs/go-netcdf/netcdf"
)

// createBaseNC is a helper to create a minimal NetCDF with common setup.
// It does NOT call EndDef - that must be done by the caller after adding all variables.
func createBaseNC(t *testing.T, path string) (f netcdf.Dataset, latDim netcdf.Dim, lonDim netcdf.Dim) {
	t.Helper()
	//nolint:gosec // G301: Standard test directory permissions.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var err error
	f, err = netcdf.CreateFile(path, netcdf.CLOBBER)
	if err != nil {
		t.Fatalf("create nc: %v", err)
	}

	latDim, _ = f.AddDim("lat", 2)
	lonDim, _ = f.AddDim("lon", 2)
	_, _ = f.AddVar("lat", netcdf.DOUBLE, []netcdf.Dim{latDim})
	_, _ = f.AddVar("lon", netcdf.DOUBLE, []netcdf.Dim{lonDim})

	return f, latDim, lonDim
}

func add2DVar(t *testing.T, f netcdf.Dataset, varName string, latDim, lonDim netcdf.Dim) netcdf.Var {
	t.Helper()
	v, err := f.AddVar(varName, netcdf.FLOAT, []netcdf.Dim{latDim, lonDim})
	if err != nil {
		t.Fatalf("add var %s: %v", varName, err)
	}
	return v
}

func write2DVar(t *testing.T, v netcdf.Var, varName string, values [][]float32) {
	t.Helper()
	flat := []float32{values[0][0], values[0][1], values[1][0], values[1][1]}
	if err := v.WriteFloat32s(flat); err != nil {
		t.Fatalf("write %s: %v", varName, err)
	}
}

// finalizeTwoVarNC completes a NetCDF file with two 2D variables by calling EndDef and writing lat/lon coordinates.
func finalizeTwoVarNC(t *testing.T, f netcdf.Dataset, v1, v2 netcdf.Var, v1Name string, v1Data [][]float32, v2Name string, v2Data [][]float32) {
	t.Helper()
	if err := f.EndDef(); err != nil {
		t.Fatalf("enddef: %v", err)
	}

	vlat, _ := f.Var("lat")
	vlon, _ := f.Var("lon")
	if err := vlat.WriteFloat64s([]float64{35.0, 36.0}); err != nil {
		t.Fatalf("write lat: %v", err)
	}
	if err := vlon.WriteFloat64s([]float64{139.0, 140.0}); err != nil {
		t.Fatalf("write lon: %v", err)
	}

	write2DVar(t, v1, v1Name, v1Data)
	write2DVar(t, v2, v2Name, v2Data)
}

// createCombinedAmpPhaseNC creates a minimal combined NetCDF with lat, lon, amplitude, phase (2x2).
func createCombinedAmpPhaseNC(t *testing.T, path string, amp [][]float32, phase [][]float32) {
	t.Helper()
	f, latDim, lonDim := createBaseNC(t, path)
	defer func() { _ = f.Close() }()

	vAmp := add2DVar(t, f, "amplitude", latDim, lonDim)
	vPhase := add2DVar(t, f, "phase", latDim, lonDim)

	finalizeTwoVarNC(t, f, vAmp, vPhase, "amplitude", amp, "phase", phase)
}

func createAmpOnlyNC(t *testing.T, path string, values [][]float32) {
	t.Helper()
	f, latDim, lonDim := createBaseNC(t, path)
	defer func() { _ = f.Close() }()

	vAmp := add2DVar(t, f, "amplitude", latDim, lonDim)

	if err := f.EndDef(); err != nil {
		t.Fatalf("enddef: %v", err)
	}

	vlat, _ := f.Var("lat")
	vlon, _ := f.Var("lon")
	if err := vlat.WriteFloat64s([]float64{35.0, 36.0}); err != nil {
		t.Fatalf("write lat: %v", err)
	}
	if err := vlon.WriteFloat64s([]float64{139.0, 140.0}); err != nil {
		t.Fatalf("write lon: %v", err)
	}

	write2DVar(t, vAmp, "amplitude", values)
}

func createPhaseOnlyNC(t *testing.T, path string, values [][]float32) {
	t.Helper()
	f, latDim, lonDim := createBaseNC(t, path)
	defer func() { _ = f.Close() }()

	vPhase := add2DVar(t, f, "phase", latDim, lonDim)

	if err := f.EndDef(); err != nil {
		t.Fatalf("enddef: %v", err)
	}

	vlat, _ := f.Var("lat")
	vlon, _ := f.Var("lon")
	if err := vlat.WriteFloat64s([]float64{35.0, 36.0}); err != nil {
		t.Fatalf("write lat: %v", err)
	}
	if err := vlon.WriteFloat64s([]float64{139.0, 140.0}); err != nil {
		t.Fatalf("write lon: %v", err)
	}

	write2DVar(t, vPhase, "phase", values)
}

// createCombinedReImNC creates a minimal combined NetCDF with lat, lon, hRe, hIm (2x2).
func createCombinedReImNC(t *testing.T, path string, re [][]float32, im [][]float32) {
	t.Helper()
	f, latDim, lonDim := createBaseNC(t, path)
	defer func() { _ = f.Close() }()

	vRe := add2DVar(t, f, "hRe", latDim, lonDim)
	vIm := add2DVar(t, f, "hIm", latDim, lonDim)

	finalizeTwoVarNC(t, f, vRe, vIm, "hRe", re, "hIm", im)
}

func TestGetAvailableConstituents_RecursiveDetectsShallow(t *testing.T) {
	dir := t.TempDir()
	// Create empty files to test name-based detection recursively
	//nolint:gosec // G301: Standard test directory permissions.
	if err := os.MkdirAll(filepath.Join(dir, "ocean_tide"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"m2_amplitude.nc", "ocean_tide/m4.nc", "ocean_tide/ms4.nc"} {
		p := filepath.Join(dir, name)
		//nolint:gosec // G306: Test file with standard permissions.
		if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	s := NewStore(dir)
	got, err := s.GetAvailableConstituents()
	if err != nil {
		t.Fatalf("GetAvailableConstituents error: %v", err)
	}
	// Expect M2, M4, MS4 at least
	want := map[string]bool{"M2": true, "M4": true, "MS4": true}
	m := map[string]bool{}
	for _, c := range got {
		m[c] = true
	}
	for k := range want {
		if !m[k] {
			t.Fatalf("expected constituent %s to be detected, got %v", k, got)
		}
	}
}

func TestLoadConstituent_SingleFileAmpPhase_CmToM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ocean_tide", "s4.nc")
	// amplitude in cm: [[100, 200], [300, 400]] -> meters [[1,2],[3,4]]
	createCombinedAmpPhaseNC(t, path,
		[][]float32{{100, 200}, {300, 400}},
		[][]float32{{10, 20}, {30, 40}},
	)
	s := NewStore(dir)
	grid, err := s.loadConstituent("S4")
	if err != nil {
		t.Fatalf("loadConstituent: %v", err)
	}
	if grid == nil || grid.Amplitude == nil || grid.Phase == nil {
		t.Fatalf("nil grids")
	}
	if grid.Amplitude.Values[0][0] != 1.0 || grid.Amplitude.Values[1][1] != 4.0 {
		t.Fatalf("amplitude not converted to meters: got %v", grid.Amplitude.Values)
	}
}

func TestLoadConstituent_SingleFileReIm_Derived(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ocean_tide", "m6.nc")
	// re/im such that amplitude hypot -> [[5, 13], [17, 25]] cm -> meters [[0.05, 0.13], ...] after conversion
	createCombinedReImNC(t, path,
		[][]float32{{3, 5}, {8, 7}},
		[][]float32{{4, 12}, {15, 24}},
	)
	s := NewStore(dir)
	grid, err := s.loadConstituent("M6")
	if err != nil {
		t.Fatalf("loadConstituent: %v", err)
	}
	if grid == nil || grid.Amplitude == nil || grid.Phase == nil {
		t.Fatalf("nil grids")
	}
	// check top-left amplitude ≈ 5 cm -> 0.05 m
	if got := grid.Amplitude.Values[0][0]; got < 0.049 || got > 0.051 {
		t.Fatalf("expected ~0.05 m, got %v", got)
	}
}

func TestLoadForLocation_WrapsNegativeLongitude(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m2.nc")
	createCombinedAmpPhaseNC(t, path,
		[][]float32{{1, 2}, {3, 4}},
		[][]float32{{10, 20}, {30, 40}},
	)
	s := NewStore(dir)
	const lon = -220.0 // Equivalent to 140° once wrapped into [0, 360)
	params, err := s.LoadForLocation(35.5, lon)
	if err != nil {
		t.Fatalf("LoadForLocation failed: %v", err)
	}
	if len(params) == 0 {
		t.Fatalf("expected at least one constituent")
	}
	if params[0].AmplitudeM <= 0 {
		t.Fatalf("expected positive amplitude, got %+v", params[0])
	}
}

func TestLoadConstituent_PrefersCombinedGlobalFile(t *testing.T) {
	dir := t.TempDir()
	createAmpOnlyNC(t, filepath.Join(dir, "q1_amplitude.nc"), [][]float32{{100, 100}, {100, 100}})
	createPhaseOnlyNC(t, filepath.Join(dir, "q1_phase.nc"), [][]float32{{10, 10}, {10, 10}})
	globalAmp := [][]float32{{1, 2}, {3, 4}}
	globalPhase := [][]float32{{11, 12}, {13, 14}}
	createCombinedAmpPhaseNC(t, filepath.Join(dir, "ocean_tide", "q1.nc"), globalAmp, globalPhase)

	s := NewStore(dir)
	grid, err := s.loadConstituent("Q1")
	if err != nil {
		t.Fatalf("loadConstituent: %v", err)
	}
	if got := grid.Amplitude.Values[0][0]; got != float64(globalAmp[0][0])/100.0 {
		t.Fatalf("expected combined file amplitude 0.01, got %v", got)
	}
}
