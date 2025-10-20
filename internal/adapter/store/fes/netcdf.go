package fes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fhs/go-netcdf/netcdf"
	"tide-api/internal/adapter/interp"
	"tide-api/internal/domain"
)

// FESStore provides access to FES2014/2022 NetCDF tidal constituent data
type FESStore struct {
	dataDir string
	cache   map[string]*FESGrid // Cache loaded grids
	mu      sync.RWMutex        // Protect cache
}

// FESGrid holds amplitude and phase grids for a constituent
type FESGrid struct {
	Name      string
	Amplitude *interp.Grid2D
	Phase     *interp.Grid2D
}

// FESFileConfig defines the expected NetCDF file structure
type FESFileConfig struct {
	// File naming patterns
	AmplitudePattern string // e.g., "{constituent}_amplitude.nc"
	PhasePattern     string // e.g., "{constituent}_phase.nc"

	// Variable names in NetCDF files
	LatVarName       string // e.g., "lat", "latitude"
	LonVarName       string // e.g., "lon", "longitude"
	AmplitudeVarName string // e.g., "amplitude", "amp"
	PhaseVarName     string // e.g., "phase", "pha"
}

// DefaultFESConfig returns the default FES file configuration
func DefaultFESConfig() FESFileConfig {
	return FESFileConfig{
		AmplitudePattern: "{constituent}_amplitude.nc",
		PhasePattern:     "{constituent}_phase.nc",
		LatVarName:       "lat",
		LonVarName:       "lon",
		AmplitudeVarName: "amplitude",
		PhaseVarName:     "phase",
	}
}

// NewFESStore creates a new FES NetCDF store
func NewFESStore(dataDir string) *FESStore {
	return &FESStore{
		dataDir: dataDir,
		cache:   make(map[string]*FESGrid),
	}
}

// LoadForLocation loads constituent parameters for a lat/lon location
// using bilinear interpolation from FES NetCDF grids
func (s *FESStore) LoadForLocation(lat, lon float64) ([]domain.ConstituentParam, error) {
	// Get available constituents
	constituents, err := s.GetAvailableConstituents()
	if err != nil {
		return nil, fmt.Errorf("failed to get available constituents: %w", err)
	}

	if len(constituents) == 0 {
		return nil, fmt.Errorf("no FES NetCDF files found in %s", s.dataDir)
	}

	// Load and interpolate each constituent
	params := make([]domain.ConstituentParam, 0, len(constituents))

	for _, constName := range constituents {
		// Load grid (uses cache if available)
		grid, err := s.loadConstituent(constName)
		if err != nil {
			// Skip constituents that fail to load (log warning in production)
			continue
		}

		// Interpolate amplitude and phase at (lat, lon)
		amplitude, phase, err := interp.InterpolateBoth(grid.Amplitude, grid.Phase, lon, lat)
		if err != nil {
			return nil, fmt.Errorf("failed to interpolate %s at (%.4f, %.4f): %w", constName, lat, lon, err)
		}

		// Get angular speed
		speed, ok := domain.GetConstituentSpeed(constName)
		if !ok {
			// Skip unknown constituents
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

// LoadForStation is not supported by FES store (only lat/lon queries)
func (s *FESStore) LoadForStation(stationID string) ([]domain.ConstituentParam, error) {
	return nil, fmt.Errorf("FES store does not support station_id queries - use lat/lon parameters")
}

// GetAvailableConstituents returns the list of constituents available in FES data
func (s *FESStore) GetAvailableConstituents() ([]string, error) {
	// Check if dataDir exists
	if _, err := os.Stat(s.dataDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("FES data directory does not exist: %s", s.dataDir)
	}

	// Scan directory for NetCDF files
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read FES directory: %w", err)
	}

	// Map to store unique constituent names
	constituentMap := make(map[string]bool)

	// Look for amplitude files (e.g., m2_amplitude.nc)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".nc") {
			continue
		}

		// Extract constituent name
		// Support patterns: m2_amplitude.nc, m2.nc, m2_amp.nc
		baseName := strings.TrimSuffix(name, ".nc")

		// Remove common suffixes
		for _, suffix := range []string{"_amplitude", "_amp", "_phase", "_pha"} {
			baseName = strings.TrimSuffix(baseName, suffix)
		}

		if baseName != "" {
			// Convert to uppercase for consistency (M2, S2, etc.)
			constName := strings.ToUpper(baseName)

			// Verify it's a known constituent
			if _, ok := domain.GetConstituentSpeed(constName); ok {
				constituentMap[constName] = true
			}
		}
	}

	// Convert map to slice
	constituents := make([]string, 0, len(constituentMap))
	for name := range constituentMap {
		constituents = append(constituents, name)
	}

	return constituents, nil
}

// loadConstituent loads amplitude and phase grids for a constituent
func (s *FESStore) loadConstituent(name string) (*FESGrid, error) {
	// Check cache first
	s.mu.RLock()
	if grid, ok := s.cache[name]; ok {
		s.mu.RUnlock()
		return grid, nil
	}
	s.mu.RUnlock()

	// Load from NetCDF files
	config := DefaultFESConfig()

	// Construct file paths (try multiple patterns)
	nameLower := strings.ToLower(name)
	ampPaths := []string{
		filepath.Join(s.dataDir, fmt.Sprintf("%s_amplitude.nc", nameLower)),
		filepath.Join(s.dataDir, fmt.Sprintf("%s_amp.nc", nameLower)),
		filepath.Join(s.dataDir, fmt.Sprintf("%s.nc", nameLower)),
	}

	phaPaths := []string{
		filepath.Join(s.dataDir, fmt.Sprintf("%s_phase.nc", nameLower)),
		filepath.Join(s.dataDir, fmt.Sprintf("%s_pha.nc", nameLower)),
		filepath.Join(s.dataDir, fmt.Sprintf("%s.nc", nameLower)), // Combined file
	}

	// Find amplitude file
	var ampPath string
	for _, path := range ampPaths {
		if _, err := os.Stat(path); err == nil {
			ampPath = path
			break
		}
	}
	if ampPath == "" {
		return nil, fmt.Errorf("amplitude file not found for constituent %s", name)
	}

	// Find phase file
	var phaPath string
	for _, path := range phaPaths {
		if _, err := os.Stat(path); err == nil {
			phaPath = path
			break
		}
	}
	if phaPath == "" {
		return nil, fmt.Errorf("phase file not found for constituent %s", name)
	}

	// Load amplitude grid
	ampGrid, err := loadNetCDFGrid(ampPath, config.LatVarName, config.LonVarName, config.AmplitudeVarName)
	if err != nil {
		return nil, fmt.Errorf("failed to load amplitude for %s: %w", name, err)
	}

	// Load phase grid
	phaGrid, err := loadNetCDFGrid(phaPath, config.LatVarName, config.LonVarName, config.PhaseVarName)
	if err != nil {
		return nil, fmt.Errorf("failed to load phase for %s: %w", name, err)
	}

	// Create FES grid
	grid := &FESGrid{
		Name:      name,
		Amplitude: ampGrid,
		Phase:     phaGrid,
	}

	// Cache the grid
	s.mu.Lock()
	s.cache[name] = grid
	s.mu.Unlock()

	return grid, nil
}

// loadNetCDFGrid reads a 2D grid from a NetCDF file
func loadNetCDFGrid(filepath, latVarName, lonVarName, dataVarName string) (*interp.Grid2D, error) {
	// Open NetCDF file
	nc, err := netcdf.OpenFile(filepath, netcdf.NOWRITE)
	if err != nil {
		return nil, fmt.Errorf("failed to open NetCDF file: %w", err)
	}
	defer nc.Close()

	// Try multiple variable name patterns
	latNames := []string{latVarName, "latitude", "lat", "y"}
	lonNames := []string{lonVarName, "longitude", "lon", "x"}
	dataNames := []string{dataVarName, "data", "z"}

	// Read latitude
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

	// Read longitude
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

	// Read data variable
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
		return nil, fmt.Errorf("data variable not found (tried: %v)", dataNames)
	}

	// Read 2D data array
	dims, err := dataVar.Dims()
	if err != nil {
		return nil, fmt.Errorf("failed to get dimensions: %w", err)
	}
	if len(dims) != 2 {
		return nil, fmt.Errorf("expected 2D data, got %dD", len(dims))
	}

	// Determine which dimension is lat and which is lon
	nLat := len(latData)
	nLon := len(lonData)

	dim0Len, _ := dims[0].Len()
	dim1Len, _ := dims[1].Len()

	// Read data based on dimension order
	var values [][]float64

	if dim0Len == uint64(nLat) && dim1Len == uint64(nLon) {
		// Data is [lat, lon]
		values, err = read2DFloat64Var(dataVar, nLat, nLon)
	} else if dim0Len == uint64(nLon) && dim1Len == uint64(nLat) {
		// Data is [lon, lat] - need to transpose
		transposed, err := read2DFloat64Var(dataVar, nLon, nLat)
		if err != nil {
			return nil, err
		}
		values = transpose2D(transposed)
	} else {
		return nil, fmt.Errorf("dimension mismatch: data is [%d, %d], expected [%d, %d] or [%d, %d]",
			dim0Len, dim1Len, nLat, nLon, nLon, nLat)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	// Create Grid2D
	grid := &interp.Grid2D{
		X:      lonData,
		Y:      latData,
		Values: values,
	}

	// Validate grid
	if err := grid.Validate(); err != nil {
		return nil, fmt.Errorf("invalid grid: %w", err)
	}

	return grid, nil
}

// readFloat64Var reads a 1D float64 array from a NetCDF variable
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

	data := make([]float64, length)
	err = v.ReadFloat64s(data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// read2DFloat64Var reads a 2D float64 array from a NetCDF variable
func read2DFloat64Var(v netcdf.Var, nRows, nCols int) ([][]float64, error) {
	// Read as flat array
	flatData := make([]float64, nRows*nCols)
	err := v.ReadFloat64s(flatData)
	if err != nil {
		return nil, err
	}

	// Convert to 2D array
	values := make([][]float64, nRows)
	for i := 0; i < nRows; i++ {
		values[i] = flatData[i*nCols : (i+1)*nCols]
	}

	return values, nil
}

// transpose2D transposes a 2D array
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
