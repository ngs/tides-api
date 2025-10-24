package bathymetry

import (
	"fmt"
	"os"
	"sync"

	"github.com/fhs/go-netcdf/netcdf"

	"go.ngs.io/tides-api/internal/adapter/interp"
	"go.ngs.io/tides-api/internal/domain"
)

// LocalStore loads bathymetry and MSL data from local NetCDF files.
// These files can be local disk files or GCS FUSE-mounted files.
type LocalStore struct {
	gebcoPath string // Path to GEBCO NetCDF file (e.g., /mnt/bathymetry/gebco_2024.nc).
	mssPath   string // Path to MSS NetCDF file (e.g., /mnt/bathymetry/dtu21_mss.nc).

	// Cached grids (loaded on demand).
	depthGrid *interp.Grid2D
	mslGrid   *interp.Grid2D
	mu        sync.RWMutex
}

// NewLocalStore creates a new local file-based bathymetry store.
// Paths can point to GCS FUSE-mounted files (e.g., /mnt/bathymetry/data.nc).
func NewLocalStore(gebcoPath, mssPath string) *LocalStore {
	return &LocalStore{
		gebcoPath: gebcoPath,
		mssPath:   mssPath,
	}
}

// GetMetadata retrieves bathymetry and MSL data for a location.
func (s *LocalStore) GetMetadata(lat, lon float64) (*domain.LocationMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load MSL grid if not cached.
	if s.mslGrid == nil && s.mssPath != "" {
		if err := s.loadMSSGrid(); err != nil {
			// MSL is optional - log warning but continue.
			fmt.Fprintf(os.Stderr, "Warning: failed to load MSS grid: %v\n", err)
		}
	}

	// Load depth grid if not cached.
	if s.depthGrid == nil && s.gebcoPath != "" {
		if err := s.loadDepthGrid(); err != nil {
			// Depth is optional - log warning but continue.
			fmt.Fprintf(os.Stderr, "Warning: failed to load depth grid: %v\n", err)
		}
	}

	// If no grids are available, return nil.
	if s.mslGrid == nil && s.depthGrid == nil {
		return nil, nil
	}

	metadata := &domain.LocationMetadata{
		MSL:        0.0,
		DatumName:  "EGM2008",
		SourceName: "Local/GCS FUSE",
	}

	// Interpolate MSL.
	if s.mslGrid != nil {
		msl, err := s.mslGrid.InterpolateAt(lon, lat)
		if err != nil {
			// If interpolation fails (e.g., out of bounds), return nil.
			return nil, nil
		}
		metadata.MSL = msl
		metadata.SourceName = "DTU21 MSS"
	}

	// Interpolate depth.
	if s.depthGrid != nil {
		depth, err := s.depthGrid.InterpolateAt(lon, lat)
		if err != nil {
			// If interpolation fails, depth remains nil.
		} else {
			// GEBCO uses negative values for depth below sea level.
			// Convert to positive depth.
			if depth < 0 {
				positiveDepth := -depth
				metadata.DepthM = &positiveDepth
			}
			if metadata.SourceName == "DTU21 MSS" {
				metadata.SourceName = "GEBCO 2025 + DTU21 MSS"
			} else {
				metadata.SourceName = "GEBCO 2025"
			}
		}
	}

	return metadata, nil
}

// loadMSSGrid loads the MSS NetCDF file.
func (s *LocalStore) loadMSSGrid() error {
	// Load NetCDF grid.
	// DTU21 uses "mean_sea_surf_sol2" variable name.
	grid, err := loadNetCDFGrid(s.mssPath, "lat", "lon", "mean_sea_surf_sol2")
	if err != nil {
		return fmt.Errorf("failed to load MSS grid: %w", err)
	}

	s.mslGrid = grid
	return nil
}

// loadDepthGrid loads the GEBCO NetCDF file.
func (s *LocalStore) loadDepthGrid() error {
	// Load NetCDF grid.
	// GEBCO uses "elevation" variable (negative for depth below sea level).
	grid, err := loadNetCDFGrid(s.gebcoPath, "lat", "lon", "elevation")
	if err != nil {
		return fmt.Errorf("failed to load GEBCO grid: %w", err)
	}

	s.depthGrid = grid
	return nil
}

// Close releases resources (no-op for local store).
func (s *LocalStore) Close() error {
	return nil
}

// loadNetCDFGrid reads a 2D grid from a NetCDF file.
// This is similar to the FES loader but with different variable names.
func loadNetCDFGrid(filepath, latVarName, lonVarName, dataVarName string) (*interp.Grid2D, error) {
	// Open NetCDF file.
	nc, err := netcdf.OpenFile(filepath, netcdf.NOWRITE)
	if err != nil {
		return nil, fmt.Errorf("failed to open NetCDF file: %w", err)
	}
	defer nc.Close()

	// Try multiple variable name patterns.
	latNames := []string{latVarName, "latitude", "lat", "y"}
	lonNames := []string{lonVarName, "longitude", "lon", "x"}
	dataNames := []string{dataVarName, "data", "z"}

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
		return nil, fmt.Errorf("data variable not found (tried: %v)", dataNames)
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

	if dim0Len == uint64(nLat) && dim1Len == uint64(nLon) {
		// Data is [lat, lon].
		values, err = read2DFloat64Var(dataVar, nLat, nLon)
	} else if dim0Len == uint64(nLon) && dim1Len == uint64(nLat) {
		// Data is [lon, lat] - need to transpose.
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

	data := make([]float64, length)
	err = v.ReadFloat64s(data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// read2DFloat64Var reads a 2D float64 array from a NetCDF variable.
// Supports float64, float32, int32, and int16 types, with optional scale_factor.
func read2DFloat64Var(v netcdf.Var, nRows, nCols int) ([][]float64, error) {
	// Get variable type.
	varType, err := v.Type()
	if err != nil {
		return nil, fmt.Errorf("failed to get variable type: %w", err)
	}

	var flatData []float64
	totalSize := nRows * nCols

	// Read data based on type.
	switch varType {
	case netcdf.DOUBLE, netcdf.FLOAT:
		// Read as float64 directly.
		flatData = make([]float64, totalSize)
		err = v.ReadFloat64s(flatData)
		if err != nil {
			return nil, fmt.Errorf("failed to read float64: %w", err)
		}
	case netcdf.SHORT:
		// Read as int16 and convert to float64.
		int16Data := make([]int16, totalSize)
		err = v.ReadInt16s(int16Data)
		if err != nil {
			return nil, fmt.Errorf("failed to read int16: %w", err)
		}
		// Convert int16 to float64.
		flatData = make([]float64, totalSize)
		for i, val := range int16Data {
			flatData[i] = float64(val)
		}
	case netcdf.INT:
		// Read as int32 and convert to float64.
		int32Data := make([]int32, totalSize)
		err = v.ReadInt32s(int32Data)
		if err != nil {
			return nil, fmt.Errorf("failed to read int32: %w", err)
		}
		// Convert int32 to float64.
		flatData = make([]float64, totalSize)
		for i, val := range int32Data {
			flatData[i] = float64(val)
		}
	default:
		return nil, fmt.Errorf("unsupported data type: %v (expected DOUBLE, FLOAT, INT, or SHORT)", varType)
	}

	// Apply scale_factor if present.
	scaleAttr := v.Attr("scale_factor")
	attrLen, err := scaleAttr.Len()
	if err == nil && attrLen > 0 {
		// scale_factor attribute exists.
		var scaleVal float64

		// Try reading as float64 first.
		scaleData := make([]float64, 1)
		err = scaleAttr.ReadFloat64s(scaleData)
		if err == nil {
			scaleVal = scaleData[0]
		} else {
			// If ReadFloat64s failed, try int32.
			int32Data := make([]int32, 1)
			err = scaleAttr.ReadInt32s(int32Data)
			if err == nil {
				scaleVal = float64(int32Data[0])
			}
		}

		if err == nil && scaleVal != 0 {
			// Apply scale factor to all values.
			for i := range flatData {
				flatData[i] *= scaleVal
			}
		}
	}

	// Convert to 2D array.
	values := make([][]float64, nRows)
	for i := 0; i < nRows; i++ {
		values[i] = flatData[i*nCols : (i+1)*nCols]
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
