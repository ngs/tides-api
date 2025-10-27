// Package fes provides access to FES2014/2022 NetCDF tidal constituent data.
package fes

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fhs/go-netcdf/netcdf"

	"go.ngs.io/tides-api/internal/adapter/interp"
	"go.ngs.io/tides-api/internal/domain"
)

const (
	amplitudeVarName = "amplitude"
)

// Store provides access to FES2014/2022 NetCDF tidal constituent data.
type Store struct {
	dataDir string
	cache   map[string]*Grid // Cache loaded grids.
	mu      sync.RWMutex     // Protect cache.
}

// Grid holds amplitude and phase grids for a constituent.
type Grid struct {
	Name      string
	Amplitude *interp.Grid2D
	Phase     *interp.Grid2D
}

// FileConfig defines the expected NetCDF file structure.
type FileConfig struct {
	// File naming patterns.
	AmplitudePattern string // E.g., "{constituent}_amplitude.nc".
	PhasePattern     string // E.g., "{constituent}_phase.nc".

	// Variable names in NetCDF files.
	LatVarName       string // E.g., "lat", "latitude".
	LonVarName       string // E.g., "lon", "longitude".
	AmplitudeVarName string // E.g., "amplitude", "amp".
	PhaseVarName     string // E.g., "phase", "pha".
}

// DefaultConfig returns the default FES file configuration.
func DefaultConfig() FileConfig {
	return FileConfig{
		AmplitudePattern: "{constituent}_amplitude.nc",
		PhasePattern:     "{constituent}_phase.nc",
		LatVarName:       "lat",
		LonVarName:       "lon",
		AmplitudeVarName: amplitudeVarName,
		PhaseVarName:     "phase",
	}
}

// NewStore creates a new FES NetCDF store.
func NewStore(dataDir string) *Store {
	return &Store{
		dataDir: dataDir,
		cache:   make(map[string]*Grid),
	}
}

// LoadForLocation loads constituent parameters for a lat/lon location
// using bilinear interpolation from FES NetCDF grids.
// NOTE: Does NOT cache grids to avoid OOM in Cloud Run.
func (s *Store) LoadForLocation(lat, lon float64) ([]domain.ConstituentParam, error) {
	// Load constituents based on location.
	// Major 8 constituents provide ~95% of tidal signal in deep water.
	// For shallow water areas, include overtide constituents (M4, M6, MS4, MN4).
	majorConstituents := []string{"M2", "S2", "N2", "K2", "K1", "O1", "P1", "Q1"}

	// Add shallow water constituents for coastal/shallow areas.
	// Check if we're in a potentially shallow area (this is a heuristic).
	// A more sophisticated approach would use bathymetry data.
	shallowWaterConstituents := []string{"M4", "MS4", "MN4", "S4"}

	// Include shallow water constituents for all requests to maintain accuracy.
	requestedConstituents := make([]string, 0, len(majorConstituents)+len(shallowWaterConstituents))
	requestedConstituents = append(requestedConstituents, majorConstituents...)
	requestedConstituents = append(requestedConstituents, shallowWaterConstituents...)

	// Verify at least some constituents are available.
	available, err := s.GetAvailableConstituents()
	if err != nil {
		return nil, fmt.Errorf("failed to get available constituents: %w", err)
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("no FES NetCDF files found in %s", s.dataDir)
	}

	// Use only constituents that exist in the data directory.
	constituents := make([]string, 0, len(requestedConstituents))
	availableMap := make(map[string]bool)
	for _, c := range available {
		availableMap[c] = true
	}
	for _, c := range requestedConstituents {
		if availableMap[c] {
			constituents = append(constituents, c)
		}
	}

	// Load and interpolate each constituent.
	params := make([]domain.ConstituentParam, 0, len(constituents))

	for _, constName := range constituents {
		// Load constituent WITHOUT caching to avoid OOM.
		// Each request reads only the 4 grid points needed for bilinear interpolation.
		amplitude, phase, err := s.interpolateConstituentAtPoint(constName, lat, lon)
		if err != nil {
			// Skip constituents that fail to load (log warning in production).
			continue
		}

		// Get angular speed.
		speed, ok := domain.GetConstituentSpeed(constName)
		if !ok {
			// Skip unknown constituents.
			continue
		}

		params = append(params, domain.ConstituentParam{
			Name:          constName,
			AmplitudeM:    amplitude,
			PhaseDeg:      phase,
			SpeedDegPerHr: speed,
		})
	}

	if len(params) == 0 {
		return nil, fmt.Errorf("no valid constituents found for location (%.4f, %.4f)", lat, lon)
	}

	return params, nil
}

// normalizeLon360 maps arbitrary degree longitudes into the [0, 360) range.
//
// FES grids are defined on a 0–360° longitude axis, so requests using the
// conventional −180–180° representation must be wrapped before interpolation.
func normalizeLon360(lon float64) float64 {
	lon = math.Mod(lon, 360.0)
	if lon < 0 {
		lon += 360.0
	}
	return lon
}

// LoadForStation is not supported by FES store (only lat/lon queries).
func (s *Store) LoadForStation(_ string) ([]domain.ConstituentParam, error) {
	return nil, fmt.Errorf("FES store does not support station_id queries - use lat/lon parameters")
}

// GetAvailableConstituents returns the list of constituents available in FES data.
func (s *Store) GetAvailableConstituents() ([]string, error) {
	// Check if dataDir exists.
	if _, err := os.Stat(s.dataDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("FES data directory does not exist: %s", s.dataDir)
	}

	// Map to store unique constituent names.
	constituentMap := make(map[string]bool)

	// Recursively walk directory for NetCDF files.
	err := filepath.WalkDir(s.dataDir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".nc") {
			return nil
		}
		baseName := strings.TrimSuffix(name, ".nc")
		for _, suffix := range []string{"_amplitude", "_amp", "_phase", "_pha"} {
			baseName = strings.TrimSuffix(baseName, suffix)
		}
		if baseName == "" {
			return nil
		}
		constName := strings.ToUpper(baseName)
		if _, ok := domain.GetConstituentSpeed(constName); ok {
			constituentMap[constName] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk FES directory: %w", err)
	}

	// Ensure shallow-water constituents are considered if corresponding files exist.
	ensure := []string{"m4", "ms4", "mn4", "m6", "s4", "mk3"}
	for _, base := range ensure {
		found := false
		// Look for combined file first (e.g., m4.nc)
		_ = filepath.WalkDir(s.dataDir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil || found {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if strings.EqualFold(d.Name(), base+".nc") || strings.EqualFold(d.Name(), base+"_amplitude.nc") {
				found = true
			}
			return nil
		})
		if found {
			upper := strings.ToUpper(base)
			if _, ok := domain.GetConstituentSpeed(upper); ok {
				constituentMap[upper] = true
			}
		}
	}

	// Convert map to slice.
	constituents := make([]string, 0, len(constituentMap))
	for name := range constituentMap {
		constituents = append(constituents, name)
	}

	return constituents, nil
}

// findFirstFile searches for the first matching file from a list of candidates.
// It performs a case-insensitive search under the given base directory.
func (s *Store) findFirstFile(candidates []string) (string, error) {
	findByName := func(target string) (string, bool, error) {
		var match string
		var found bool
		err := filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.EqualFold(d.Name(), target) {
				match = path
				found = true
				return fs.SkipAll
			}
			return nil
		})
		if err != nil {
			return "", false, err
		}
		return match, found, nil
	}

	for _, candidate := range candidates {
		path, found, err := findByName(candidate)
		if err != nil {
			return "", err
		}
		if found {
			return path, nil
		}
	}
	return "", fmt.Errorf("not found")
}

// interpolateConstituentAtPoint reads only the 4 grid points needed for bilinear interpolation.
// This avoids loading entire grids (which can be 100+ MB each) into memory.
func (s *Store) interpolateConstituentAtPoint(name string, lat, lon float64) (amplitude, phase float64, err error) {
	nameLower := strings.ToLower(name)
	config := DefaultConfig()

	// Find amplitude and phase files.
	ampCandidates := []string{
		fmt.Sprintf("ocean_tide/%s.nc", nameLower),
		fmt.Sprintf("%s.nc", nameLower),
		fmt.Sprintf("%s_amplitude.nc", nameLower),
		fmt.Sprintf("%s_amp.nc", nameLower),
	}
	phaCandidates := []string{
		fmt.Sprintf("ocean_tide/%s.nc", nameLower),
		fmt.Sprintf("%s.nc", nameLower),
		fmt.Sprintf("%s_phase.nc", nameLower),
		fmt.Sprintf("%s_pha.nc", nameLower),
	}

	ampPath, err := s.findFirstFile(ampCandidates)
	if err != nil {
		return 0, 0, fmt.Errorf("amplitude file not found for constituent %s", name)
	}
	phaPath, err := s.findFirstFile(phaCandidates)
	if err != nil {
		return 0, 0, fmt.Errorf("phase file not found for constituent %s", name)
	}

	// Read amplitude and phase at the specific lat/lon (only 4 points each).
	normLon := normalizeLon360(lon)
	amplitude, err = interpolatePointFromNetCDF(ampPath, config.LatVarName, config.LonVarName, config.AmplitudeVarName, lat, normLon)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to interpolate amplitude: %w", err)
	}
	phase, err = interpolatePointFromNetCDF(phaPath, config.LatVarName, config.LonVarName, config.PhaseVarName, lat, normLon)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to interpolate phase: %w", err)
	}

	// Convert cm to meters.
	amplitude /= 100.0

	return amplitude, phase, nil
}

// loadConstituent loads amplitude and phase grids for a constituent.
// Deprecated: Loads entire grids into memory. Use interpolateConstituentAtPoint instead.
func (s *Store) loadConstituent(name string) (*Grid, error) {
	// Check cache first.
	s.mu.RLock()
	if grid, ok := s.cache[name]; ok {
		s.mu.RUnlock()
		return grid, nil
	}
	s.mu.RUnlock()

	// Load from NetCDF files.
	config := DefaultConfig()

	// Build candidate base names and search recursively under dataDir.
	nameLower := strings.ToLower(name)
	ampCandidates := []string{
		fmt.Sprintf("%s.nc", nameLower), // Combined file (global coverage)
		fmt.Sprintf("%s_amplitude.nc", nameLower),
		fmt.Sprintf("%s_amp.nc", nameLower),
	}
	phaCandidates := []string{
		fmt.Sprintf("%s.nc", nameLower), // Combined file (global coverage)
		fmt.Sprintf("%s_phase.nc", nameLower),
		fmt.Sprintf("%s_pha.nc", nameLower),
	}

	ampPath, err := s.findFirstFile(ampCandidates)
	if err != nil {
		return nil, fmt.Errorf("amplitude file not found for constituent %s", name)
	}
	phaPath, err := s.findFirstFile(phaCandidates)
	if err != nil {
		return nil, fmt.Errorf("phase file not found for constituent %s", name)
	}

	// Load amplitude grid.
	ampGrid, err := loadNetCDFGrid(ampPath, config.LatVarName, config.LonVarName, config.AmplitudeVarName)
	if err != nil {
		return nil, fmt.Errorf("failed to load amplitude for %s: %w", name, err)
	}

	// Load phase grid.
	phaGrid, err := loadNetCDFGrid(phaPath, config.LatVarName, config.LonVarName, config.PhaseVarName)
	if err != nil {
		return nil, fmt.Errorf("failed to load phase for %s: %w", name, err)
	}

	// Create FES grid.
	grid := &Grid{
		Name:      name,
		Amplitude: ampGrid,
		Phase:     phaGrid,
	}

	// Cache the grid.
	s.mu.Lock()
	s.cache[name] = grid
	s.mu.Unlock()

	return grid, nil
}

// interpolatePointFromNetCDF reads only 4 grid points around (lat, lon) and interpolates.
// This minimizes memory usage by avoiding loading entire grids.
//
//nolint:gocyclo,nestif // Complex NetCDF subset reading logic with multiple fallback paths.
func interpolatePointFromNetCDF(filepath, latVarName, lonVarName, dataVarName string, lat, lon float64) (float64, error) {
	// Open NetCDF file.
	nc, err := netcdf.OpenFile(filepath, netcdf.NOWRITE)
	if err != nil {
		return 0, fmt.Errorf("failed to open NetCDF file: %w", err)
	}
	defer func() { _ = nc.Close() }()

	// Try multiple variable name patterns.
	latNames := []string{latVarName, "latitude", "lat", "y"}
	lonNames := []string{lonVarName, "longitude", "lon", "x"}

	// Read full coordinate arrays (these are small: 1D arrays of ~2881 and ~5760 points).
	var latData []float64
	var latFound bool
	for _, name := range latNames {
		if v, err := nc.Var(name); err == nil {
			latData, err = readFloat64Var(v)
			if err == nil {
				latFound = true
				break
			}
		}
	}
	if !latFound {
		return 0, fmt.Errorf("latitude variable not found (tried: %v)", latNames)
	}

	var lonData []float64
	var lonFound bool
	for _, name := range lonNames {
		if v, err := nc.Var(name); err == nil {
			lonData, err = readFloat64Var(v)
			if err == nil {
				lonFound = true
				break
			}
		}
	}
	if !lonFound {
		return 0, fmt.Errorf("longitude variable not found (tried: %v)", lonNames)
	}

	// Find grid cell indices surrounding the target point.
	// latData and lonData should be monotonically increasing.
	latIdx := findGridCell(latData, lat)
	lonIdx := findGridCell(lonData, lon)

	if latIdx < 0 || lonIdx < 0 {
		return 0, fmt.Errorf("point (%.4f, %.4f) outside grid bounds", lat, lon)
	}

	// Build candidate data variable names.
	lower := strings.ToLower(dataVarName)
	dataNames := []string{}
	if dataVarName != "" {
		dataNames = append(dataNames, dataVarName)
	}
	if strings.Contains(lower, "amp") || strings.Contains(lower, "ampl") {
		dataNames = append(dataNames,
			"amplitude", "Amplitude", "amp", "Amp",
			"HA", "Ha", "ha", "H", "h",
		)
	} else if strings.Contains(lower, "pha") || strings.Contains(lower, "phase") {
		dataNames = append(dataNames,
			"phase", "Phase", "pha", "Pha",
			"Hg", "HG", "hg", "g", "G",
			"phi", "Phi", "PHI", "phase_deg",
		)
	}
	dataNames = append(dataNames, "data", "z")

	// Find data variable.
	var dataVar netcdf.Var
	var dataFound bool
	for _, name := range dataNames {
		if v, err := nc.Var(name); err == nil {
			dataVar = v
			dataFound = true
			break
		}
	}
	if !dataFound {
		// Try complex pair (real/imag).
		realCandidates := []string{"hRe", "Hre", "hre", "Re", "RE", "real", "Real"}
		imagCandidates := []string{"hIm", "Him", "him", "Im", "IM", "imag", "Imag"}

		var realVar, imagVar netcdf.Var
		var haveRe, haveIm bool
		for _, rn := range realCandidates {
			if v, err := nc.Var(rn); err == nil {
				realVar = v
				haveRe = true
				break
			}
		}
		for _, in := range imagCandidates {
			if v, err := nc.Var(in); err == nil {
				imagVar = v
				haveIm = true
				break
			}
		}
		if !haveRe || !haveIm {
			return 0, fmt.Errorf("data variable not found (tried: %v), and no complex pair detected", dataNames)
		}

		// Read 2x2 subset from real and imag.
		reVals, err := readSubset2x2(realVar, len(latData), len(lonData), latIdx, lonIdx)
		if err != nil {
			return 0, fmt.Errorf("failed to read real subset: %w", err)
		}
		imVals, err := readSubset2x2(imagVar, len(latData), len(lonData), latIdx, lonIdx)
		if err != nil {
			return 0, fmt.Errorf("failed to read imag subset: %w", err)
		}

		// Handle fill values.
		if fv, ok := getFillValue(realVar); ok {
			for i := range reVals {
				for j := range reVals[i] {
					if reVals[i][j] == fv {
						reVals[i][j] = 0
					}
				}
			}
		}
		if fv, ok := getFillValue(imagVar); ok {
			for i := range imVals {
				for j := range imVals[i] {
					if imVals[i][j] == fv {
						imVals[i][j] = 0
					}
				}
			}
		}

		// Compute amplitude or phase.
		want := strings.ToLower(dataVarName)
		values := make([][]float64, 2)
		for i := 0; i < 2; i++ {
			values[i] = make([]float64, 2)
			for j := 0; j < 2; j++ {
				re := reVals[i][j]
				im := imVals[i][j]
				if strings.Contains(want, "amp") || strings.Contains(want, "ampl") || want == amplitudeVarName {
					values[i][j] = math.Hypot(re, im)
				} else {
					deg := domain.Rad2Deg(math.Atan2(im, re))
					if deg < 0 {
						deg += 360.0
					}
					values[i][j] = deg
				}
			}
		}

		// Apply cm->m conversion for amplitude from ocean_tide combined files.
		if (strings.Contains(want, "amp") || want == amplitudeVarName) && strings.Contains(strings.ToLower(filepath), "ocean_tide") {
			for i := range values {
				for j := range values[i] {
					values[i][j] /= 100.0
				}
			}
		}

		// Bilinear interpolation.
		return bilinearInterpolate(latData[latIdx:latIdx+2], lonData[lonIdx:lonIdx+2], values, lat, lon), nil
	}

	// Read 2x2 subset from data variable.
	values, err := readSubset2x2(dataVar, len(latData), len(lonData), latIdx, lonIdx)
	if err != nil {
		return 0, fmt.Errorf("failed to read data subset: %w", err)
	}

	// Handle fill values.
	if fv, ok := getFillValue(dataVar); ok {
		for i := range values {
			for j := range values[i] {
				if values[i][j] == fv {
					values[i][j] = 0
				}
			}
		}
	}

	// Unit conversion for amplitude grids.
	if (strings.Contains(strings.ToLower(dataVarName), "amp") || strings.ToLower(dataVarName) == amplitudeVarName) &&
		strings.Contains(strings.ToLower(filepath), "ocean_tide") {
		for i := range values {
			for j := range values[i] {
				values[i][j] /= 100.0
			}
		}
	}

	// Bilinear interpolation.
	return bilinearInterpolate(latData[latIdx:latIdx+2], lonData[lonIdx:lonIdx+2], values, lat, lon), nil
}

// findGridCell finds the index of the grid cell containing the given coordinate value.
// Returns the lower index of the cell (i such that coords[i] <= val < coords[i+1]).
// Returns -1 if val is outside the grid bounds.
func findGridCell(coords []float64, val float64) int {
	n := len(coords)
	if n < 2 {
		return -1
	}

	// Check bounds.
	if val < coords[0] || val > coords[n-1] {
		return -1
	}

	// Binary search for the cell.
	left, right := 0, n-1
	for left < right-1 {
		mid := (left + right) / 2
		if coords[mid] <= val {
			left = mid
		} else {
			right = mid
		}
	}

	return left
}

// readSubset2x2 reads a 2x2 subset from a NetCDF variable.
// It reads data[latIdx:latIdx+2, lonIdx:lonIdx+2].
//
//nolint:nestif // Type checking for NetCDF variable requires nested switch.
func readSubset2x2(v netcdf.Var, nLat, nLon, latIdx, lonIdx int) ([][]float64, error) {
	// Verify indices are valid.
	if latIdx < 0 || latIdx >= nLat-1 || lonIdx < 0 || lonIdx >= nLon-1 {
		return nil, fmt.Errorf("invalid indices: latIdx=%d, lonIdx=%d, nLat=%d, nLon=%d", latIdx, lonIdx, nLat, nLon)
	}

	// Check dimensions to determine if data is [lat, lon] or [lon, lat].
	dims, err := v.Dims()
	if err != nil {
		return nil, fmt.Errorf("failed to get dimensions: %w", err)
	}
	if len(dims) != 2 {
		return nil, fmt.Errorf("expected 2D variable, got %dD", len(dims))
	}

	dim0Len, err := dims[0].Len()
	if err != nil {
		return nil, fmt.Errorf("failed to get dim0 length: %w", err)
	}
	dim1Len, err := dims[1].Len()
	if err != nil {
		return nil, fmt.Errorf("failed to get dim1 length: %w", err)
	}

	// Determine dimension order and read subset.
	var flat []float64
	var needTranspose bool

	type dimPair struct{ d0, d1 uint64 }
	switch (dimPair{dim0Len, dim1Len}) {
	case dimPair{uint64(nLat), uint64(nLon)}:
		// Data is [lat, lon] - read directly.
		flat, err = readSubsetFlat(v, latIdx, lonIdx, 2, 2)
		needTranspose = false
	case dimPair{uint64(nLon), uint64(nLat)}:
		// Data is [lon, lat] - read transposed.
		flat, err = readSubsetFlat(v, lonIdx, latIdx, 2, 2)
		needTranspose = true
	default:
		return nil, fmt.Errorf("dimension mismatch: data is [%d, %d], expected [%d, %d] or [%d, %d]",
			dim0Len, dim1Len, nLat, nLon, nLon, nLat)
	}

	if err != nil {
		return nil, err
	}

	// Convert flat array to 2D.
	values := make([][]float64, 2)
	if needTranspose {
		// flat is [lon, lat], need to transpose to [lat, lon].
		values[0] = []float64{flat[0], flat[2]}
		values[1] = []float64{flat[1], flat[3]}
	} else {
		// flat is [lat, lon].
		values[0] = flat[0:2]
		values[1] = flat[2:4]
	}

	return values, nil
}

// readSubsetFlat reads a 2D subset from a NetCDF variable as a flat array.
// It reads data[start0:start0+count0, start1:start1+count1].
func readSubsetFlat(v netcdf.Var, start0, start1, count0, count1 int) ([]float64, error) {
	total := count0 * count1

	// Get variable type and read subset.
	t, err := v.Type()
	if err != nil {
		return nil, fmt.Errorf("failed to get var type: %w", err)
	}

	switch t {
	case netcdf.DOUBLE:
		flat := make([]float64, total)
		if err := v.ReadFloat64Slice(flat, []uint64{uint64(start0), uint64(start1)}, []uint64{uint64(count0), uint64(count1)}); err != nil {
			return nil, err
		}
		return flat, nil
	case netcdf.FLOAT:
		tmp := make([]float32, total)
		if err := v.ReadFloat32Slice(tmp, []uint64{uint64(start0), uint64(start1)}, []uint64{uint64(count0), uint64(count1)}); err != nil {
			return nil, err
		}
		flat := make([]float64, total)
		for i, val := range tmp {
			flat[i] = float64(val)
		}
		return flat, nil
	case netcdf.INT:
		tmp := make([]int32, total)
		if err := v.ReadInt32Slice(tmp, []uint64{uint64(start0), uint64(start1)}, []uint64{uint64(count0), uint64(count1)}); err != nil {
			return nil, err
		}
		flat := make([]float64, total)
		for i, val := range tmp {
			flat[i] = float64(val)
		}
		return flat, nil
	case netcdf.SHORT:
		tmp := make([]int16, total)
		if err := v.ReadInt16Slice(tmp, []uint64{uint64(start0), uint64(start1)}, []uint64{uint64(count0), uint64(count1)}); err != nil {
			return nil, err
		}
		flat := make([]float64, total)
		for i, val := range tmp {
			flat[i] = float64(val)
		}
		return flat, nil
	case netcdf.BYTE, netcdf.CHAR, netcdf.UBYTE, netcdf.USHORT, netcdf.UINT, netcdf.INT64, netcdf.UINT64, netcdf.STRING:
		return nil, fmt.Errorf("unsupported data type: %v", t)
	default:
		return nil, fmt.Errorf("unsupported data type: %v", t)
	}
}

// bilinearInterpolate performs bilinear interpolation on a 2x2 grid.
func bilinearInterpolate(lats, lons []float64, values [][]float64, lat, lon float64) float64 {
	// Normalize coordinates to [0, 1].
	dx := (lon - lons[0]) / (lons[1] - lons[0])
	dy := (lat - lats[0]) / (lats[1] - lats[0])

	// Bilinear interpolation formula.
	v00 := values[0][0]
	v01 := values[0][1]
	v10 := values[1][0]
	v11 := values[1][1]

	return (1-dx)*(1-dy)*v00 + dx*(1-dy)*v01 + (1-dx)*dy*v10 + dx*dy*v11
}

// loadNetCDFGrid reads a 2D grid from a NetCDF file.
//
//nolint:gocyclo,nestif,gosec // Complex NetCDF loading logic with many variable name patterns.
func loadNetCDFGrid(filepath, latVarName, lonVarName, dataVarName string) (*interp.Grid2D, error) {
	// Open NetCDF file.
	nc, err := netcdf.OpenFile(filepath, netcdf.NOWRITE)
	if err != nil {
		return nil, fmt.Errorf("failed to open NetCDF file: %w", err)
	}
	defer func() { _ = nc.Close() }()

	// Try multiple variable name patterns.
	latNames := []string{latVarName, "latitude", "lat", "y"}
	lonNames := []string{lonVarName, "longitude", "lon", "x"}

	// Build candidate data variable names. Expand to include common FES names.
	lower := strings.ToLower(dataVarName)
	dataNames := []string{}
	// Always try the provided name first
	if dataVarName != "" {
		dataNames = append(dataNames, dataVarName)
	}
	// Heuristics for amplitude vs phase
	if strings.Contains(lower, "amp") || strings.Contains(lower, "ampl") {
		dataNames = append(dataNames,
			"amplitude", "Amplitude", "amp", "Amp",
			"HA", "Ha", "ha", "H", "h",
		)
	} else if strings.Contains(lower, "pha") || strings.Contains(lower, "phase") {
		dataNames = append(dataNames,
			"phase", "Phase", "pha", "Pha",
			"Hg", "HG", "hg", "g", "G",
			"phi", "Phi", "PHI", "phase_deg",
		)
	}
	// Generic fallbacks
	dataNames = append(dataNames, "data", "z")

	// Read latitude.
	var latData []float64
	var latFound bool
	for _, name := range latNames {
		if v, err := nc.Var(name); err == nil {
			latData, err = readFloat64Var(v)
			if err == nil {
				latFound = true
				break
			}
		}
	}
	if !latFound {
		return nil, fmt.Errorf("latitude variable not found (tried: %v)", latNames)
	}

	// Read longitude.
	var lonData []float64
	var lonFound bool
	for _, name := range lonNames {
		if v, err := nc.Var(name); err == nil {
			lonData, err = readFloat64Var(v)
			if err == nil {
				lonFound = true
				break
			}
		}
	}
	if !lonFound {
		return nil, fmt.Errorf("longitude variable not found (tried: %v)", lonNames)
	}

	// Read data variable.
	var dataVar netcdf.Var
	var dataFound bool
	for _, name := range dataNames {
		if v, err := nc.Var(name); err == nil {
			dataVar = v
			dataFound = true
			break
		}
	}
	if !dataFound {
		// Fallback: try complex pair variables (real/imag) and derive amplitude or phase.
		// Common candidate names.
		realCandidates := []string{"hRe", "Hre", "hre", "Re", "RE", "real", "Real"}
		imagCandidates := []string{"hIm", "Him", "him", "Im", "IM", "imag", "Imag"}

		var realVar, imagVar netcdf.Var
		var haveRe, haveIm bool
		for _, rn := range realCandidates {
			if v, err := nc.Var(rn); err == nil {
				realVar = v
				haveRe = true
				break
			}
		}
		for _, in := range imagCandidates {
			if v, err := nc.Var(in); err == nil {
				imagVar = v
				haveIm = true
				break
			}
		}
		if !haveRe || !haveIm {
			return nil, fmt.Errorf("data variable not found (tried: %v), and no complex pair detected", dataNames)
		}

		// Helper to read 2D var matching lat/lon orientation.
		read2D := func(v netcdf.Var, nLat, nLon int) ([][]float64, error) {
			dims, err := v.Dims()
			if err != nil {
				return nil, fmt.Errorf("failed to get dimensions: %w", err)
			}
			if len(dims) != 2 {
				return nil, fmt.Errorf("expected 2D data, got %dD", len(dims))
			}
			dim0Len, err := dims[0].Len()
			if err != nil {
				return nil, fmt.Errorf("failed to get dim0 length: %w", err)
			}
			dim1Len, err := dims[1].Len()
			if err != nil {
				return nil, fmt.Errorf("failed to get dim1 length: %w", err)
			}
			if dim0Len == uint64(nLat) && dim1Len == uint64(nLon) {
				return read2DFloat64Var(v, nLat, nLon)
			}
			if dim0Len == uint64(nLon) && dim1Len == uint64(nLat) {
				transposed, err := read2DFloat64Var(v, nLon, nLat)
				if err != nil {
					return nil, err
				}
				return transpose2D(transposed), nil
			}
			return nil, fmt.Errorf("dimension mismatch for complex var: data is [%d, %d], expected [%d, %d] or [%d, %d]",
				dim0Len, dim1Len, nLat, nLon, nLon, nLat)
		}

		nLat := len(latData)
		nLon := len(lonData)
		reVals, err := read2D(realVar, nLat, nLon)
		if err != nil {
			return nil, fmt.Errorf("failed to read real component: %w", err)
		}
		imVals, err := read2D(imagVar, nLat, nLon)
		if err != nil {
			return nil, fmt.Errorf("failed to read imag component: %w", err)
		}

		// Handle fill values for complex components (replace with 0).
		if fv, ok := getFillValue(realVar); ok {
			for i := range reVals {
				for j := range reVals[i] {
					if reVals[i][j] == fv {
						reVals[i][j] = 0
					}
				}
			}
		}
		if fv, ok := getFillValue(imagVar); ok {
			for i := range imVals {
				for j := range imVals[i] {
					if imVals[i][j] == fv {
						imVals[i][j] = 0
					}
				}
			}
		}

		// Decide whether amplitude or phase is requested based on dataVarName hint.
		want := strings.ToLower(dataVarName)
		values := make([][]float64, nLat)
		for i := 0; i < nLat; i++ {
			values[i] = make([]float64, nLon)
			for j := 0; j < nLon; j++ {
				re := reVals[i][j]
				im := imVals[i][j]
				if strings.Contains(want, "amp") || strings.Contains(want, "ampl") || want == amplitudeVarName {
					// Amplitude = sqrt(re^2 + im^2)
					values[i][j] = math.Hypot(re, im)
				} else {
					// Phase (degrees) = atan2(im, re) mapped to [0, 360)
					deg := domain.Rad2Deg(math.Atan2(im, re))
					if deg < 0 {
						deg += 360.0
					}
					values[i][j] = deg
				}
			}
		}

		// Apply cm->m conversion for amplitude from ocean_tide combined files.
		if (strings.Contains(want, "amp") || want == amplitudeVarName) && strings.Contains(strings.ToLower(filepath), "ocean_tide") {
			for i := range values {
				for j := range values[i] {
					values[i][j] /= 100.0
				}
			}
		}

		grid := &interp.Grid2D{X: lonData, Y: latData, Values: values}
		if err := grid.Validate(); err != nil {
			return nil, fmt.Errorf("invalid grid: %w", err)
		}
		return grid, nil
	}

	// Read 2D data array.
	dims, err := dataVar.Dims()
	if err != nil {
		return nil, fmt.Errorf("failed to get dimensions: %w", err)
	}
	if len(dims) != 2 {
		return nil, fmt.Errorf("expected 2D data, got %dD", len(dims))
	}

	// Determine which dimension is lat and which is lon.
	nLat := len(latData)
	nLon := len(lonData)

	dim0Len, err := dims[0].Len()
	if err != nil {
		return nil, fmt.Errorf("failed to get dim0 length: %w", err)
	}
	dim1Len, err := dims[1].Len()
	if err != nil {
		return nil, fmt.Errorf("failed to get dim1 length: %w", err)
	}

	// Read data based on dimension order.
	var values [][]float64

	type dimOrder struct{ d0, d1 uint64 }
	switch (dimOrder{dim0Len, dim1Len}) {
	case dimOrder{uint64(nLat), uint64(nLon)}:
		// Data is [lat, lon].
		values, err = read2DFloat64Var(dataVar, nLat, nLon)
	case dimOrder{uint64(nLon), uint64(nLat)}:
		// Data is [lon, lat] - need to transpose.
		transposed, err := read2DFloat64Var(dataVar, nLon, nLat)
		if err != nil {
			return nil, err
		}
		values = transpose2D(transposed)
	default:
		return nil, fmt.Errorf("dimension mismatch: data is [%d, %d], expected [%d, %d] or [%d, %d]",
			dim0Len, dim1Len, nLat, nLon, nLon, nLat)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	// Replace _FillValue or missing_value with 0 to avoid huge artifacts.
	if fv, ok := getFillValue(dataVar); ok {
		for i := range values {
			for j := range values[i] {
				if values[i][j] == fv {
					values[i][j] = 0
				}
			}
		}
	}

	// Unit conversion for amplitude grids: known FES ocean_tide files use centimeters.
	// If reading from ocean_tide path and variable name indicates amplitude, convert cm->m.
	if (strings.Contains(strings.ToLower(dataVarName), "amp") || strings.ToLower(dataVarName) == amplitudeVarName) &&
		strings.Contains(strings.ToLower(filepath), "ocean_tide") {
		for i := range values {
			for j := range values[i] {
				values[i][j] /= 100.0
			}
		}
	}

	// Create Grid2D.
	grid := &interp.Grid2D{
		X:      lonData,
		Y:      latData,
		Values: values,
	}

	// Validate grid.
	if err := grid.Validate(); err != nil {
		return nil, fmt.Errorf("invalid grid: %w", err)
	}

	return grid, nil
}

// getFillValue returns the _FillValue or missing_value attribute if present as float64.
func getFillValue(v netcdf.Var) (float64, bool) {
	for _, name := range []string{"_FillValue", "missing_value"} {
		a := v.Attr(name)
		if a == (netcdf.Attr{}) {
			continue
		}
		if n, err := a.Len(); err == nil && n > 0 {
			// Try float64
			buf64 := make([]float64, 1)
			if err := a.ReadFloat64s(buf64); err == nil {
				return buf64[0], true
			}
			// Try float32
			buf32 := make([]float32, 1)
			if err := a.ReadFloat32s(buf32); err == nil {
				return float64(buf32[0]), true
			}
			// Try int32
			bufi := make([]int32, 1)
			if err := a.ReadInt32s(bufi); err == nil {
				return float64(bufi[0]), true
			}
		}
	}
	return 0, false
}

// readFloat64Var reads a 1D float64 array from a NetCDF variable.
func readFloat64Var(v netcdf.Var) ([]float64, error) {
	dims, err := v.Dims()
	if err != nil {
		return nil, fmt.Errorf("failed to get dimensions: %w", err)
	}
	if len(dims) != 1 {
		return nil, fmt.Errorf("expected 1D variable, got %dD", len(dims))
	}

	length, err := dims[0].Len()
	if err != nil {
		return nil, err
	}

	if t, err := v.Type(); err == nil {
		switch t {
		case netcdf.DOUBLE:
			data := make([]float64, length)
			if err := v.ReadFloat64s(data); err != nil {
				return nil, err
			}
			return data, nil
		case netcdf.FLOAT:
			tmp := make([]float32, length)
			if err := v.ReadFloat32s(tmp); err != nil {
				return nil, err
			}
			out := make([]float64, length)
			for i, val := range tmp {
				out[i] = float64(val)
			}
			return out, nil
		case netcdf.INT:
			tmp := make([]int32, length)
			if err := v.ReadInt32s(tmp); err != nil {
				return nil, err
			}
			out := make([]float64, length)
			for i, val := range tmp {
				out[i] = float64(val)
			}
			return out, nil
		case netcdf.SHORT:
			tmp := make([]int16, length)
			if err := v.ReadInt16s(tmp); err != nil {
				return nil, err
			}
			out := make([]float64, length)
			for i, val := range tmp {
				out[i] = float64(val)
			}
			return out, nil
		case netcdf.BYTE, netcdf.CHAR, netcdf.UBYTE, netcdf.USHORT, netcdf.UINT, netcdf.INT64, netcdf.UINT64, netcdf.STRING:
			return nil, fmt.Errorf("unsupported var type: %v", t)
		default:
			return nil, fmt.Errorf("unsupported var type: %v", t)
		}
	}
	return nil, fmt.Errorf("failed to get var type: %v", err)
}

// read2DFloat64Var reads a 2D float64 array from a NetCDF variable.
func read2DFloat64Var(v netcdf.Var, nRows, nCols int) ([][]float64, error) {
	total := nRows * nCols
	var flat []float64
	//nolint:nestif // Type checking for NetCDF variable requires nested switch.
	if t, err := v.Type(); err == nil {
		switch t {
		case netcdf.DOUBLE:
			flat = make([]float64, total)
			if err := v.ReadFloat64s(flat); err != nil {
				return nil, err
			}
		case netcdf.FLOAT:
			tmp := make([]float32, total)
			if err := v.ReadFloat32s(tmp); err != nil {
				return nil, err
			}
			flat = make([]float64, total)
			for i, val := range tmp {
				flat[i] = float64(val)
			}
		case netcdf.INT:
			tmp := make([]int32, total)
			if err := v.ReadInt32s(tmp); err != nil {
				return nil, err
			}
			flat = make([]float64, total)
			for i, val := range tmp {
				flat[i] = float64(val)
			}
		case netcdf.SHORT:
			tmp := make([]int16, total)
			if err := v.ReadInt16s(tmp); err != nil {
				return nil, err
			}
			flat = make([]float64, total)
			for i, val := range tmp {
				flat[i] = float64(val)
			}
		case netcdf.BYTE, netcdf.CHAR, netcdf.UBYTE, netcdf.USHORT, netcdf.UINT, netcdf.INT64, netcdf.UINT64, netcdf.STRING:
			return nil, fmt.Errorf("unsupported data type: %v", t)
		default:
			return nil, fmt.Errorf("unsupported data type: %v", t)
		}
	} else {
		return nil, fmt.Errorf("failed to get var type: %v", err)
	}

	values := make([][]float64, nRows)
	for i := 0; i < nRows; i++ {
		values[i] = flat[i*nCols : (i+1)*nCols]
	}
	return values, nil
}

// transpose2D transposes a 2D array.
func transpose2D(data [][]float64) [][]float64 {
	if len(data) == 0 {
		return data
	}

	nRows := len(data)
	nCols := len(data[0])

	transposed := make([][]float64, nCols)
	for i := 0; i < nCols; i++ {
		transposed[i] = make([]float64, nRows)
		for j := 0; j < nRows; j++ {
			transposed[i][j] = data[j][i]
		}
	}

	return transposed
}
