package fes

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/fhs/go-netcdf/netcdf"
)

// helper to create a minimal combined NetCDF with lat, lon, amplitude, phase (2x2)
func createCombinedAmpPhaseNC(t *testing.T, path string, amp [][]float32, phase [][]float32) {
    t.Helper()
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    f, err := netcdf.CreateFile(path, netcdf.CLOBBER)
    if err != nil {
        t.Fatalf("create nc: %v", err)
    }
    defer f.Close()

    latDim, _ := f.AddDim("lat", 2)
    lonDim, _ := f.AddDim("lon", 2)
    vlat, _ := f.AddVar("lat", netcdf.DOUBLE, []netcdf.Dim{latDim})
    vlon, _ := f.AddVar("lon", netcdf.DOUBLE, []netcdf.Dim{lonDim})
    vamp, _ := f.AddVar("amplitude", netcdf.FLOAT, []netcdf.Dim{latDim, lonDim})
    vpha, _ := f.AddVar("phase", netcdf.FLOAT, []netcdf.Dim{latDim, lonDim})

    if err := f.EndDef(); err != nil { t.Fatalf("enddef: %v", err) }

    if err := vlat.WriteFloat64s([]float64{35.0, 36.0}); err != nil { t.Fatalf("write lat: %v", err) }
    if err := vlon.WriteFloat64s([]float64{139.0, 140.0}); err != nil { t.Fatalf("write lon: %v", err) }
    aFlat := []float32{amp[0][0], amp[0][1], amp[1][0], amp[1][1]}
    pFlat := []float32{phase[0][0], phase[0][1], phase[1][0], phase[1][1]}
    if err := vamp.WriteFloat32s(aFlat); err != nil { t.Fatalf("write amp: %v", err) }
    if err := vpha.WriteFloat32s(pFlat); err != nil { t.Fatalf("write pha: %v", err) }
}

// helper to create a minimal combined NetCDF with lat, lon, hRe, hIm (2x2)
func createCombinedReImNC(t *testing.T, path string, re [][]float32, im [][]float32) {
    t.Helper()
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    f, err := netcdf.CreateFile(path, netcdf.CLOBBER)
    if err != nil {
        t.Fatalf("create nc: %v", err)
    }
    defer f.Close()

    latDim, _ := f.AddDim("lat", 2)
    lonDim, _ := f.AddDim("lon", 2)
    vlat, _ := f.AddVar("lat", netcdf.DOUBLE, []netcdf.Dim{latDim})
    vlon, _ := f.AddVar("lon", netcdf.DOUBLE, []netcdf.Dim{lonDim})
    vre, _ := f.AddVar("hRe", netcdf.FLOAT, []netcdf.Dim{latDim, lonDim})
    vim, _ := f.AddVar("hIm", netcdf.FLOAT, []netcdf.Dim{latDim, lonDim})

    if err := f.EndDef(); err != nil { t.Fatalf("enddef: %v", err) }

    if err := vlat.WriteFloat64s([]float64{35.0, 36.0}); err != nil { t.Fatalf("write lat: %v", err) }
    if err := vlon.WriteFloat64s([]float64{139.0, 140.0}); err != nil { t.Fatalf("write lon: %v", err) }
    rFlat := []float32{re[0][0], re[0][1], re[1][0], re[1][1]}
    iFlat := []float32{im[0][0], im[0][1], im[1][0], im[1][1]}
    if err := vre.WriteFloat32s(rFlat); err != nil { t.Fatalf("write re: %v", err) }
    if err := vim.WriteFloat32s(iFlat); err != nil { t.Fatalf("write im: %v", err) }
}

func TestGetAvailableConstituents_RecursiveDetectsShallow(t *testing.T) {
    dir := t.TempDir()
    // Create empty files to test name-based detection recursively
    if err := os.MkdirAll(filepath.Join(dir, "ocean_tide"), 0o755); err != nil { t.Fatal(err) }
    for _, name := range []string{"m2_amplitude.nc", "ocean_tide/m4.nc", "ocean_tide/ms4.nc"} {
        p := filepath.Join(dir, name)
        if err := os.WriteFile(p, []byte{}, 0o644); err != nil { t.Fatalf("write %s: %v", name, err) }
    }
    s := NewFESStore(dir)
    got, err := s.GetAvailableConstituents()
    if err != nil { t.Fatalf("GetAvailableConstituents error: %v", err) }
    // Expect M2, M4, MS4 at least
    want := map[string]bool{"M2": true, "M4": true, "MS4": true}
    m := map[string]bool{}
    for _, c := range got { m[c] = true }
    for k := range want {
        if !m[k] { t.Fatalf("expected constituent %s to be detected, got %v", k, got) }
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
    s := NewFESStore(dir)
    grid, err := s.loadConstituent("S4")
    if err != nil { t.Fatalf("loadConstituent: %v", err) }
    if grid == nil || grid.Amplitude == nil || grid.Phase == nil { t.Fatalf("nil grids") }
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
    s := NewFESStore(dir)
    grid, err := s.loadConstituent("M6")
    if err != nil { t.Fatalf("loadConstituent: %v", err) }
    if grid == nil || grid.Amplitude == nil || grid.Phase == nil { t.Fatalf("nil grids") }
    // check top-left amplitude ≈ 5 cm -> 0.05 m
    if got := grid.Amplitude.Values[0][0]; got < 0.049 || got > 0.051 {
        t.Fatalf("expected ~0.05 m, got %v", got)
    }
}
