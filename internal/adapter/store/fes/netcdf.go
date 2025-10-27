// Package fes provides access to FES2014/2022 NetCDF tidal constituent data.
package fes

import (
	"errors"
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
func (s *Store) LoadForLocation(lat, lon float64) ([]domain.ConstituentParam, error) {
	// Get available constituents.
	constituents, err := s.GetAvailableConstituents()
	if err != nil {
		return nil, fmt.Errorf("failed to get available constituents: %w", err)
	}

	if len(constituents) == 0 {
		return nil, fmt.Errorf("no FES NetCDF files found in %s", s.dataDir)
	}

	// Load and interpolate each constituent.
	params := make([]domain.ConstituentParam, 0, len(constituents))

	for _, constName := range constituents {
		// Load grid (uses cache if available).
		grid, err := s.loadConstituent(constName)
		if err != nil {
			// Skip constituents that fail to load (log warning in production).
			continue
		}

		// Interpolate amplitude and phase at (lat, lon).
		amplitude, phase, err := interp.InterpolateBoth(grid.Amplitude, grid.Phase, normalizeLon360(lon), lat)
		if err != nil {
			return nil, fmt.Errorf("failed to interpolate %s at (%.4f, %.4f): %w", constName, lat, lon, err)
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

// loadConstituent loads amplitude and phase grids for a constituent.
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

	findFirst := func(candidates []string) (string, error) {
		errNotFound := errors.New("not found")
		findByName := func(target string) (string, error) {
			var match string
			errFound := errors.New("found")
			err := filepath.WalkDir(s.dataDir, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				if strings.EqualFold(d.Name(), target) {
					match = path
					return errFound
				}
				return nil
			})
			if err != nil {
				if err == errFound {
					return match, nil
				}
				return "", err
			}
			return "", errNotFound
		}

		for _, candidate := range candidates {
			path, err := findByName(candidate)
			if err == nil {
				return path, nil
			}
			if !errors.Is(err, errNotFound) {
				return "", err
			}
		}
		return "", fmt.Errorf("not found")
	}

	ampPath, err := findFirst(ampCandidates)
	if err != nil {
		return nil, fmt.Errorf("amplitude file not found for constituent %s", name)
	}
	phaPath, err := findFirst(phaCandidates)
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
