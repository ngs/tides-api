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
	"go.ngs.io/tides-api/internal/adapter/ncio"
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
		// Seam-stitched grids extend past 360: map wrapped values into range.
		if lonCheck < b.minLon && lonCheck+360 <= b.maxLon {
			lonCheck += 360
		}
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
	if !lonAxisRequiresWrap(lons) {
		return lon
	}
	l := normalizeLon360(lon)
	// Seam-stitched grids extend past 360: map wrapped values into the axis range.
	if len(lons) > 0 && l < lons[0] && l+360 <= lons[len(lons)-1] {
		l += 360
	}
	return l
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
	// libnetcdf is not thread-safe: serialize the whole open-read-close sequence.
	ncio.Lock()
	defer ncio.Unlock()

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

	// Determine dimension ordering of the 2D data array.
	dims, err := dataVar.Dims()
	if err != nil {
		return nil, fmt.Errorf("failed to get dimensions: %w", err)
	}
	if len(dims) != 2 {
		return nil, fmt.Errorf("expected 2D data, got %dD", len(dims))
	}

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

	var latFirst bool
	switch {
	case dim0Len == uint64(nLat) && dim1Len == uint64(nLon):
		latFirst = true
	case dim0Len == uint64(nLon) && dim1Len == uint64(nLat):
		latFirst = false
	default:
		return nil, fmt.Errorf("dimension mismatch: data is [%d, %d], expected [%d, %d] or [%d, %d]",
			dim0Len, dim1Len, nLat, nLon, nLon, nLat)
	}

	// readWindow reads a [lat, lon] oriented window regardless of the on-disk order.
	readWindow := func(latStart, lonStart, nRows, nCols int) ([][]float64, error) {
		if latFirst {
			return read2DFloat64VarSubset(dataVar, latStart, lonStart, nRows, nCols)
		}
		transposed, err := read2DFloat64VarSubset(dataVar, lonStart, latStart, nCols, nRows)
		if err != nil {
			return nil, err
		}
		return transpose2D(transposed), nil
	}

	buildGrid := func(x, y []float64, values [][]float64) (*interp.Grid2D, error) {
		grid := &interp.Grid2D{X: x, Y: y, Values: values}
		if err := grid.Validate(); err != nil {
			return nil, fmt.Errorf("invalid grid: %w", err)
		}
		return grid, nil
	}

	if margin <= 0 {
		// Load entire grid.
		values, err := readWindow(0, 0, nLat, nLon)
		if err != nil {
			return nil, fmt.Errorf("failed to read data: %w", err)
		}
		return buildGrid(lonData, latData, values)
	}

	// Latitude subset window.
	latStartIdx := findNearestIndex(latData, targetLat-margin)
	latEndIdx := findNearestIndex(latData, targetLat+margin)
	if latStartIdx > latEndIdx {
		latStartIdx, latEndIdx = latEndIdx, latStartIdx
	}
	latStart := clamp(latStartIdx, 0, nLat-2)
	latEnd := clamp(latEndIdx+1, latStart+2, nLat)
	nSubsetLat := latEnd - latStart
	subsetLat := latData[latStart:latEnd]

	adjLon := normalizeLonForAxis(lonData, targetLon)
	adjLonMinus := normalizeLon360IfWrapped(lonData, targetLon-margin)
	adjLonPlus := normalizeLon360IfWrapped(lonData, targetLon+margin)

	// Detect a subset window that crosses the 0/360 seam of a global wrapped
	// axis: the normalized west edge ends up east of the normalized east edge.
	if lonAxisRequiresWrap(lonData) && lonData[nLon-1]-lonData[0] > 180 && adjLonMinus > adjLonPlus {
		return loadSeamCrossingSubset(lonData, latStart, nSubsetLat, subsetLat, adjLonMinus, adjLonPlus, readWindow, buildGrid)
	}

	// Regular (non seam-crossing) longitude subset window.
	lonStartIdx := findNearestIndex(lonData, adjLonMinus)
	lonEndIdx := findNearestIndex(lonData, adjLonPlus)
	if lonStartIdx == lonEndIdx {
		// Ensure at least one additional column if possible.
		lonEndIdx = clamp(lonEndIdx+1, 0, nLon-1)
	}
	// If adjusted lon fell outside range (e.g., wrapped) ensure target column included.
	lonTargetIdx := findNearestIndex(lonData, adjLon)
	if lonTargetIdx < lonStartIdx {
		lonStartIdx = lonTargetIdx
	}
	if lonTargetIdx > lonEndIdx {
		lonEndIdx = lonTargetIdx
	}
	if lonStartIdx > lonEndIdx {
		lonStartIdx, lonEndIdx = lonEndIdx, lonStartIdx
	}
	lonStart := clamp(lonStartIdx, 0, nLon-2)
	lonEnd := clamp(lonEndIdx+1, lonStart+2, nLon)

	values, err := readWindow(latStart, lonStart, nSubsetLat, lonEnd-lonStart)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}
	return buildGrid(lonData[lonStart:lonEnd], subsetLat, values)
}

// normalizeLon360IfWrapped normalizes lon into [0, 360) when the axis is a
// wrapped (0..360 style) axis; otherwise returns lon unchanged.
func normalizeLon360IfWrapped(lons []float64, lon float64) float64 {
	if lonAxisRequiresWrap(lons) {
		return normalizeLon360(lon)
	}
	return lon
}

// loadSeamCrossingSubset reads a longitude window that crosses the 0/360 seam
// of a global wrapped axis. It reads two hyperslabs - the west segment
// [adjLonMinus .. end of axis] and the east segment [start of axis .. adjLonPlus] -
// and stitches them into one grid, unwrapping the east longitudes by +360 so
// the X axis stays strictly increasing.
func loadSeamCrossingSubset(
	lonData []float64,
	latStart, nSubsetLat int,
	subsetLat []float64,
	adjLonMinus, adjLonPlus float64,
	readWindow func(latStart, lonStart, nRows, nCols int) ([][]float64, error),
	buildGrid func(x, y []float64, values [][]float64) (*interp.Grid2D, error),
) (*interp.Grid2D, error) {
	nLon := len(lonData)

	westStart := clamp(findNearestIndex(lonData, adjLonMinus), 0, nLon-1)
	eastEnd := clamp(findNearestIndex(lonData, adjLonPlus), 0, nLon-1)

	// Skip east columns that would duplicate the end of the west segment
	// (e.g. an axis that contains both 0 and 360).
	eastSkip := 0
	for eastSkip <= eastEnd && lonData[eastSkip]+360 <= lonData[nLon-1]+1e-9 {
		eastSkip++
	}
	nEast := eastEnd + 1 - eastSkip
	if nEast <= 0 {
		// The west segment alone covers the window (axis includes the seam column).
		if westStart > nLon-2 {
			westStart = nLon - 2
		}
		values, err := readWindow(latStart, westStart, nSubsetLat, nLon-westStart)
		if err != nil {
			return nil, fmt.Errorf("failed to read data: %w", err)
		}
		return buildGrid(lonData[westStart:], subsetLat, values)
	}
	nWest := nLon - westStart

	westVals, err := readWindow(latStart, westStart, nSubsetLat, nWest)
	if err != nil {
		return nil, fmt.Errorf("failed to read west data segment: %w", err)
	}
	eastVals, err := readWindow(latStart, eastSkip, nSubsetLat, nEast)
	if err != nil {
		return nil, fmt.Errorf("failed to read east data segment: %w", err)
	}

	subsetLon := make([]float64, 0, nWest+nEast)
	subsetLon = append(subsetLon, lonData[westStart:]...)
	for _, lo := range lonData[eastSkip : eastEnd+1] {
		subsetLon = append(subsetLon, lo+360)
	}

	values := make([][]float64, nSubsetLat)
	for i := range values {
		row := make([]float64, 0, nWest+nEast)
		row = append(row, westVals[i]...)
		row = append(row, eastVals[i]...)
		values[i] = row
	}

	return buildGrid(subsetLon, subsetLat, values)
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

	// Apply scale_factor and add_offset if present (packed data support).
	applyScaleOffset(v, flatData)

	// Convert to 2D array.
	values := make([][]float64, nRows)
	for i := 0; i < nRows; i++ {
		values[i] = flatData[i*nCols : (i+1)*nCols]
	}

	return values, nil
}

// applyScaleOffset unpacks values using the scale_factor and add_offset
// attributes when present: true = packed*scale_factor + add_offset.
func applyScaleOffset(v netcdf.Var, flatData []float64) {
	if scale, ok := getAttrFloat(v, "scale_factor"); ok && scale != 0 {
		for i := range flatData {
			flatData[i] *= scale
		}
	}
	if offset, ok := getAttrFloat(v, "add_offset"); ok && offset != 0 {
		for i := range flatData {
			flatData[i] += offset
		}
	}
}

// getAttrFloat reads a scalar numeric attribute as float64.
func getAttrFloat(v netcdf.Var, name string) (float64, bool) {
	a := v.Attr(name)
	if a == (netcdf.Attr{}) {
		return 0, false
	}
	n, err := a.Len()
	if err != nil || n == 0 {
		return 0, false
	}
	buf64 := make([]float64, 1)
	if err := a.ReadFloat64s(buf64); err == nil {
		return buf64[0], true
	}
	buf32 := make([]float32, 1)
	if err := a.ReadFloat32s(buf32); err == nil {
		return float64(buf32[0]), true
	}
	bufi := make([]int32, 1)
	if err := a.ReadInt32s(bufi); err == nil {
		return float64(bufi[0]), true
	}
	bufs := make([]int16, 1)
	if err := a.ReadInt16s(bufs); err == nil {
		return float64(bufs[0]), true
	}
	return 0, false
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

	// Apply scale_factor and add_offset if present (packed data support).
	applyScaleOffset(v, flatData)

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
