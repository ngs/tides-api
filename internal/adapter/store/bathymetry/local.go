// Package bathymetry provides bathymetry data loading from NetCDF files.
package bathymetry

import (
	"fmt"
	"math"
	"os"
	"sync"

	"github.com/fhs/go-netcdf/netcdf"

	"go.ngs.io/tides-api/internal/adapter/geoid"
	"go.ngs.io/tides-api/internal/adapter/interp"
	"go.ngs.io/tides-api/internal/domain"
)

// LocalStore loads bathymetry and MSL data from local NetCDF files.
// These files can be local disk files or GCS FUSE-mounted files.
type LocalStore struct {
	gebcoPath  string // Path to GEBCO NetCDF file (e.g., /mnt/bathymetry/gebco_2024.nc).
	mssPath    string // Path to MSS NetCDF file (e.g., /mnt/bathymetry/dtu21_mss.nc).
	geoidStore *geoid.Store

	// Cached grids (loaded on demand).
	depthGrid   *interp.Grid2D
	depthBounds *gridBounds
	mslGrid     *interp.Grid2D
	mslBounds   *gridBounds
	mu          sync.RWMutex
}

type gridBounds struct {
	minLat, maxLat float64
	minLon, maxLon float64
	lonWrap360     bool
}

func (b *gridBounds) contains(lat, lon float64) bool {
	if b == nil {
		return false
	}
	lonCheck := lon
	if b.lonWrap360 {
		lonCheck = normalizeLon360(lonCheck)
	}
	return lat >= b.minLat && lat <= b.maxLat && lonCheck >= b.minLon && lonCheck <= b.maxLon
}

func boundsFromGrid(grid *interp.Grid2D) *gridBounds {
	if grid == nil || len(grid.X) == 0 || len(grid.Y) == 0 {
		return nil
	}
	wrap := lonAxisRequiresWrap(grid.X)
	minLon := grid.X[0]
	maxLon := grid.X[len(grid.X)-1]
	if minLon > maxLon {
		minLon, maxLon = maxLon, minLon
	}
	minLat := grid.Y[0]
	maxLat := grid.Y[len(grid.Y)-1]
	if minLat > maxLat {
		minLat, maxLat = maxLat, minLat
	}
	return &gridBounds{
		minLat:     minLat,
		maxLat:     maxLat,
		minLon:     minLon,
		maxLon:     maxLon,
		lonWrap360: wrap,
	}
}

func lonAxisRequiresWrap(lons []float64) bool {
	if len(lons) == 0 {
		return false
	}
	minVal := lons[0]
	maxVal := lons[len(lons)-1]
	if minVal > maxVal {
		minVal, maxVal = maxVal, minVal
	}
	return minVal >= 0 && maxVal > 180
}

func normalizeLon360(lon float64) float64 {
	lon = math.Mod(lon, 360)
	if lon < 0 {
		lon += 360
	}
	return lon
}

func normalizeLonForAxis(lons []float64, lon float64) float64 {
	if lonAxisRequiresWrap(lons) {
		return normalizeLon360(lon)
	}
	return lon
}

// NewLocalStore creates a new local file-based bathymetry store.
// Paths can point to GCS FUSE-mounted files (e.g., /mnt/bathymetry/data.nc).
func NewLocalStore(gebcoPath, mssPath string, geoidStore *geoid.Store) *LocalStore {
	return &LocalStore{
		gebcoPath:  gebcoPath,
		mssPath:    mssPath,
		geoidStore: geoidStore,
	}
}

// GetMetadata retrieves bathymetry and MSL data for a location.
func (s *LocalStore) GetMetadata(lat, lon float64) (*domain.LocationMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load MSL grid if needed.
	if s.mssPath != "" && (s.mslGrid == nil || !s.mslBounds.contains(lat, lon)) {
		if err := s.loadMSSGrid(lat, lon); err != nil {
			// MSL is optional - log warning but continue.
			fmt.Fprintf(os.Stderr, "Warning: failed to load MSS grid: %v\n", err)
		}
	}

	// Load depth grid if needed.
	if s.gebcoPath != "" && (s.depthGrid == nil || !s.depthBounds.contains(lat, lon)) {
		if err := s.loadDepthGrid(lat, lon); err != nil {
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
	//nolint:nestif // Grid interpolation logic with multiple error paths.
	if s.mslGrid != nil {
		lonMSL := normalizeLonForAxis(s.mslGrid.X, lon)
		msl, err := s.mslGrid.InterpolateAt(lonMSL, lat)
		if err != nil {
			// If interpolation fails (e.g., out of bounds), return nil.
			return nil, nil
		}

		// DTU21 MSS is referenced to WGS84 ellipsoid.
		// Apply geoid correction to convert to orthometric height (local datum).
		// H (orthometric) = h (ellipsoidal) - N (geoid height).
		if s.geoidStore != nil {
			geoidHeight, err := s.geoidStore.GetGeoidHeight(lat, lon)
			if err == nil {
				// Apply correction: subtract geoid height from ellipsoidal MSL.
				msl -= geoidHeight
				metadata.DatumName = "EGM2008 (geoid-corrected)"
			} else {
				// Log warning but continue with uncorrected value.
				fmt.Fprintf(os.Stderr, "Warning: geoid correction failed: %v\n", err)
			}
		}

		metadata.MSL = msl
		metadata.SourceName = "DTU21 MSS"
	}

	// Interpolate depth.
	//nolint:nestif // Grid interpolation logic with multiple conditional paths.
	if s.depthGrid != nil {
		lonDepth := normalizeLonForAxis(s.depthGrid.X, lon)
		depth, err := s.depthGrid.InterpolateAt(lonDepth, lat)
		// If interpolation fails, depth remains nil.
		if err == nil {
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

// loadMSSGrid loads a subset of the MSS NetCDF file around the target location.
func (s *LocalStore) loadMSSGrid(lat, lon float64) error {
	// Load NetCDF grid subset with ±2 degree margin.
	// DTU21 uses "mean_sea_surf_sol2" variable name.
	const margin = 2.0 // Degrees.
	grid, err := loadNetCDFGridSubset(s.mssPath, "lat", "lon", "mean_sea_surf_sol2", lat, lon, margin)
	if err != nil {
		return fmt.Errorf("failed to load MSS grid: %w", err)
	}

	s.mslGrid = grid
	s.mslBounds = boundsFromGrid(grid)
	return nil
}

// loadDepthGrid loads a subset of the GEBCO NetCDF file around the target location.
func (s *LocalStore) loadDepthGrid(lat, lon float64) error {
	// Load NetCDF grid subset with ±2 degree margin.
	// GEBCO uses "elevation" variable (negative for depth below sea level).
	const margin = 2.0 // Degrees.
	grid, err := loadNetCDFGridSubset(s.gebcoPath, "lat", "lon", "elevation", lat, lon, margin)
	if err != nil {
		return fmt.Errorf("failed to load GEBCO grid: %w", err)
	}

	s.depthGrid = grid
	s.depthBounds = boundsFromGrid(grid)
	return nil
}

// Close releases resources (no-op for local store).
func (s *LocalStore) Close() error {
	return nil
}

// loadNetCDFGridSubset reads a subset of a 2D grid from a NetCDF file.
// If margin is 0, the entire grid is loaded.
// If margin > 0, only data within ±margin degrees of (targetLat, targetLon) is loaded.
//
//nolint:gocyclo,nestif,gosec // Complex NetCDF loading logic with many cases.
func loadNetCDFGridSubset(filepath, latVarName, lonVarName, dataVarName string, targetLat, targetLon, margin float64) (*interp.Grid2D, error) {
	// Open NetCDF file.
	nc, err := netcdf.OpenFile(filepath, netcdf.NOWRITE)
	if err != nil {
		return nil, fmt.Errorf("failed to open NetCDF file: %w", err)
	}
	defer func() { _ = nc.Close() }()

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

	// Calculate subset indices if margin is specified.
	var latStart, latEnd, lonStart, lonEnd int
	var subsetLat, subsetLon []float64

	if margin > 0 {
		adjLon := normalizeLonForAxis(lonData, targetLon)
		adjLonMinus := normalizeLonForAxis(lonData, targetLon-margin)
		adjLonPlus := normalizeLonForAxis(lonData, targetLon+margin)

		// Find indices for the subset region.
		latStartIdx := findNearestIndex(latData, targetLat-margin)
		latEndIdx := findNearestIndex(latData, targetLat+margin)
		lonStartIdx := findNearestIndex(lonData, adjLonMinus)
		lonEndIdx := findNearestIndex(lonData, adjLonPlus)
		if lonStartIdx == lonEndIdx {
			// Ensure at least one additional column if possible.
			lonEndIdx = clamp(lonEndIdx+1, 0, len(lonData)-1)
		}
		// If adjusted lon fell outside range (e.g., wrapped) ensure target column included.
		lonTargetIdx := findNearestIndex(lonData, adjLon)
		if lonTargetIdx < lonStartIdx {
			lonStartIdx = lonTargetIdx
		}
		if lonTargetIdx > lonEndIdx {
			lonEndIdx = lonTargetIdx
		}

		// Ensure proper ordering (start <= end).
		if latStartIdx > latEndIdx {
			latStartIdx, latEndIdx = latEndIdx, latStartIdx
		}
		if lonStartIdx > lonEndIdx {
			lonStartIdx, lonEndIdx = lonEndIdx, lonStartIdx
		}

		// Clamp to valid ranges and ensure we have at least 2 points.
		latStart = clamp(latStartIdx, 0, len(latData)-2)
		latEnd = clamp(latEndIdx+1, latStart+2, len(latData))
		lonStart = clamp(lonStartIdx, 0, len(lonData)-2)
		lonEnd = clamp(lonEndIdx+1, lonStart+2, len(lonData))

		// Extract subset of coordinate arrays.
		subsetLat = latData[latStart:latEnd]
		subsetLon = lonData[lonStart:lonEnd]
	} else {
		// Load entire grid.
		latStart, latEnd = 0, len(latData)
		lonStart, lonEnd = 0, len(lonData)
		subsetLat = latData
		subsetLon = lonData
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

	// Determine dimension ordering.
	type dimOrder int
	const (
		latLonOrder dimOrder = iota
		lonLatOrder
		unknownOrder
	)

	order := unknownOrder
	switch {
	case dim0Len == uint64(nLat) && dim1Len == uint64(nLon):
		order = latLonOrder
	case dim0Len == uint64(nLon) && dim1Len == uint64(nLat):
		order = lonLatOrder
	}

	// Calculate subset dimensions.
	nSubsetLat := latEnd - latStart
	nSubsetLon := lonEnd - lonStart

	switch order {
	case latLonOrder:
		// Data is [lat, lon].
		if margin > 0 {
			values, err = read2DFloat64VarSubset(dataVar, latStart, lonStart, nSubsetLat, nSubsetLon)
		} else {
			values, err = read2DFloat64Var(dataVar, nLat, nLon)
		}
	case lonLatOrder:
		// Data is [lon, lat] - need to transpose.
		var transposed [][]float64
		if margin > 0 {
			transposed, err = read2DFloat64VarSubset(dataVar, lonStart, latStart, nSubsetLon, nSubsetLat)
		} else {
			transposed, err = read2DFloat64Var(dataVar, nLon, nLat)
		}
		if err != nil {
			return nil, err
		}
		values = transpose2D(transposed)
	case unknownOrder:
		return nil, fmt.Errorf("dimension mismatch: data is [%d, %d], expected [%d, %d] or [%d, %d]",
			dim0Len, dim1Len, nLat, nLon, nLon, nLat)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	// Create Grid2D.
	grid := &interp.Grid2D{
		X:      subsetLon,
		Y:      subsetLat,
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
	case netcdf.DOUBLE:
		flatData = make([]float64, totalSize)
		err = v.ReadFloat64s(flatData)
		if err != nil {
			return nil, fmt.Errorf("failed to read float64: %w", err)
		}
	case netcdf.FLOAT:
		// Read as float32 and convert to float64.
		float32Data := make([]float32, totalSize)
		err = v.ReadFloat32s(float32Data)
		if err != nil {
			return nil, fmt.Errorf("failed to read float32: %w", err)
		}
		flatData = make([]float64, totalSize)
		for i, val := range float32Data {
			flatData[i] = float64(val)
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
	case netcdf.BYTE, netcdf.UBYTE, netcdf.CHAR, netcdf.USHORT, netcdf.UINT, netcdf.INT64, netcdf.UINT64, netcdf.STRING:
		return nil, fmt.Errorf("unsupported data type: %v (expected DOUBLE, FLOAT, INT, or SHORT)", varType)
	}

	// Apply scale_factor if present.
	scaleAttr := v.Attr("scale_factor")
	attrLen, err := scaleAttr.Len()
	//nolint:nestif // NetCDF attribute handling requires nested conditionals.
	if err == nil && attrLen > 0 {
		// Scale_factor attribute exists.
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

// read2DFloat64VarSubset reads a subset of a 2D float64 array from a NetCDF variable.
// Reads data starting at [startRow, startCol] with dimensions [nRows, nCols].
// Supports the same data types as read2DFloat64Var.
func read2DFloat64VarSubset(v netcdf.Var, startRow, startCol, nRows, nCols int) ([][]float64, error) {
	// Get variable type.
	varType, err := v.Type()
	if err != nil {
		return nil, fmt.Errorf("failed to get variable type: %w", err)
	}

	var flatData []float64
	totalSize := nRows * nCols

	// Prepare start and count arrays for hyperslab reading.
	//nolint:gosec // G115: Safe int to uint64 conversion for NetCDF indices.
	start := []uint64{uint64(startRow), uint64(startCol)}
	//nolint:gosec // G115: Safe int to uint64 conversion for NetCDF dimensions.
	count := []uint64{uint64(nRows), uint64(nCols)}

	// Read data based on type.
	switch varType {
	case netcdf.DOUBLE:
		flatData = make([]float64, totalSize)
		err = v.ReadFloat64Slice(flatData, start, count)
		if err != nil {
			return nil, fmt.Errorf("failed to read float64 subset: %w", err)
		}
	case netcdf.FLOAT:
		// Read as float32 and convert to float64.
		float32Data := make([]float32, totalSize)
		err = v.ReadFloat32Slice(float32Data, start, count)
		if err != nil {
			return nil, fmt.Errorf("failed to read float32 subset: %w", err)
		}
		flatData = make([]float64, totalSize)
		for i, val := range float32Data {
			flatData[i] = float64(val)
		}
	case netcdf.SHORT:
		// Read as int16 and convert to float64.
		int16Data := make([]int16, totalSize)
		err = v.ReadInt16Slice(int16Data, start, count)
		if err != nil {
			return nil, fmt.Errorf("failed to read int16 subset: %w", err)
		}
		flatData = make([]float64, totalSize)
		for i, val := range int16Data {
			flatData[i] = float64(val)
		}
	case netcdf.INT:
		// Read as int32 and convert to float64.
		int32Data := make([]int32, totalSize)
		err = v.ReadInt32Slice(int32Data, start, count)
		if err != nil {
			return nil, fmt.Errorf("failed to read int32 subset: %w", err)
		}
		flatData = make([]float64, totalSize)
		for i, val := range int32Data {
			flatData[i] = float64(val)
		}
	case netcdf.BYTE, netcdf.UBYTE, netcdf.CHAR, netcdf.USHORT, netcdf.UINT, netcdf.INT64, netcdf.UINT64, netcdf.STRING:
		return nil, fmt.Errorf("unsupported data type: %v (expected DOUBLE, FLOAT, INT, or SHORT)", varType)
	}

	// Apply scale_factor if present.
	scaleAttr := v.Attr("scale_factor")
	attrLen, err := scaleAttr.Len()
	//nolint:nestif // NetCDF attribute handling requires nested conditionals.
	if err == nil && attrLen > 0 {
		// Scale_factor attribute exists.
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

// findNearestIndex finds the index of the value closest to target in a sorted array.
func findNearestIndex(arr []float64, target float64) int {
	if len(arr) == 0 {
		return 0
	}

	// Binary search for efficiency with large arrays.
	left, right := 0, len(arr)-1

	for left < right {
		mid := (left + right) / 2
		if arr[mid] < target {
			left = mid + 1
		} else {
			right = mid
		}
	}

	// Check if left-1 is closer.
	if left > 0 && math.Abs(arr[left-1]-target) < math.Abs(arr[left]-target) {
		return left - 1
	}

	return left
}

// clamp ensures value is within [minVal, maxVal] range.
func clamp(value, minVal, maxVal int) int {
	if value < minVal {
		return minVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}
