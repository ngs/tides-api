// Package geoid provides access to EGM2008 geoid data for MSL corrections.
package geoid

import (
	"fmt"
	"math"
	"sync"

	"github.com/fhs/go-netcdf/netcdf"

	"go.ngs.io/tides-api/internal/adapter/interp"
	"go.ngs.io/tides-api/internal/adapter/ncio"
)

// Store provides geoid height lookups for coordinate transformations.
type Store struct {
	geoidPath string // Path to EGM2008 NetCDF file.
	grid      *interp.Grid2D
	bounds    *gridBounds
	mu        sync.RWMutex
}

// gridBounds describes the geographic coverage of a loaded grid subset.
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
	return &gridBounds{
		minLat:     grid.Y[0],
		maxLat:     grid.Y[len(grid.Y)-1],
		minLon:     grid.X[0],
		maxLon:     grid.X[len(grid.X)-1],
		lonWrap360: lonAxisRequiresWrap(grid.X),
	}
}

// lonAxisRequiresWrap reports whether a longitude axis uses the 0..360 convention.
func lonAxisRequiresWrap(lons []float64) bool {
	if len(lons) == 0 {
		return false
	}
	return lons[0] >= 0 && lons[len(lons)-1] > 180
}

func normalizeLon360(lon float64) float64 {
	lon = math.Mod(lon, 360)
	if lon < 0 {
		lon += 360
	}
	return lon
}

// normalizeLonForAxis maps a query longitude onto the grid's longitude axis
// convention (0..360 wrapped axes vs. -180..180 axes).
func normalizeLonForAxis(lons []float64, lon float64) float64 {
	if !lonAxisRequiresWrap(lons) {
		return lon
	}
	l := normalizeLon360(lon)
	if len(lons) > 0 && l < lons[0] && l+360 <= lons[len(lons)-1] {
		l += 360
	}
	return l
}

// NewStore creates a new geoid store.
func NewStore(geoidPath string) *Store {
	return &Store{
		geoidPath: geoidPath,
	}
}

// GetGeoidHeight returns the EGM2008 geoid height (N) at a given location.
// This is the separation between the WGS84 ellipsoid and the geoid (mean sea level).
// Positive values mean the geoid is above the ellipsoid.
//
// To convert from ellipsoidal height (h) to orthometric height (H):
//
//	H = h - N
func (s *Store) GetGeoidHeight(lat, lon float64) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load grid on first access, and reload when the requested location falls
	// outside the currently loaded subset.
	if s.grid == nil || !s.bounds.contains(lat, lon) {
		if err := s.loadGrid(lat, lon); err != nil {
			return 0, fmt.Errorf("failed to load geoid grid: %w", err)
		}
	}

	// Interpolate geoid height (normalizing the longitude to the grid axis).
	height, err := s.grid.InterpolateAt(normalizeLonForAxis(s.grid.X, lon), lat)
	if err != nil {
		return 0, fmt.Errorf("failed to interpolate geoid height: %w", err)
	}

	return height, nil
}

// loadGrid loads a subset of the EGM2008 NetCDF grid around the target location.
func (s *Store) loadGrid(targetLat, targetLon float64) error {
	// libnetcdf is not thread-safe: serialize the whole open-read-close sequence.
	ncio.Lock()
	defer ncio.Unlock()

	nc, err := netcdf.OpenFile(s.geoidPath, netcdf.NOWRITE)
	if err != nil {
		return fmt.Errorf("failed to open NetCDF file: %w", err)
	}
	defer func() { _ = nc.Close() }()

	// Try common variable names for geoid grids.
	dataNames := []string{"geoid", "geoid_height", "N", "height", "z"}

	latData, err := readCoordVar(nc, "latitude", []string{"lat", "latitude", "y"})
	if err != nil {
		return err
	}
	lonData, err := readCoordVar(nc, "longitude", []string{"lon", "longitude", "x"})
	if err != nil {
		return err
	}

	// Calculate subset indices with ±2 degree margin.
	// Normalize the target longitude to the grid axis convention first
	// (e.g. lon=-90 on a 0..360 axis maps to 270).
	const margin = 2.0 // Degrees.
	adjLon := normalizeLonForAxis(lonData, targetLon)
	latStartIdx := findNearestIndex(latData, targetLat-margin)
	latEndIdx := findNearestIndex(latData, targetLat+margin)
	lonStartIdx := findNearestIndex(lonData, adjLon-margin)
	lonEndIdx := findNearestIndex(lonData, adjLon+margin)

	// Make sure the target column itself is included.
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
	latStart := clamp(latStartIdx, 0, len(latData)-2)
	latEnd := clamp(latEndIdx+1, latStart+2, len(latData))
	lonStart := clamp(lonStartIdx, 0, len(lonData)-2)
	lonEnd := clamp(lonEndIdx+1, lonStart+2, len(lonData))

	// Extract subset of coordinate arrays.
	subsetLat := latData[latStart:latEnd]
	subsetLon := lonData[lonStart:lonEnd]

	// Read geoid height data.
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
		return fmt.Errorf("geoid data variable not found (tried: %v)", dataNames)
	}

	// Read 2D data array.
	dims, err := dataVar.Dims()
	if err != nil {
		return fmt.Errorf("failed to get dimensions: %w", err)
	}
	if len(dims) != 2 {
		return fmt.Errorf("expected 2D data, got %dD", len(dims))
	}

	nLat := len(latData)
	nLon := len(lonData)

	dim0Len, err := dims[0].Len()
	if err != nil {
		return fmt.Errorf("failed to get dim0 length: %w", err)
	}
	dim1Len, err := dims[1].Len()
	if err != nil {
		return fmt.Errorf("failed to get dim1 length: %w", err)
	}

	// Calculate subset dimensions.
	nSubsetLat := latEnd - latStart
	nSubsetLon := lonEnd - lonStart

	// Determine dimension ordering.
	var values [][]float64
	switch {
	case dim0Len == uint64(nLat) && dim1Len == uint64(nLon):
		// Data is [lat, lon].
		values, err = read2DFloat64VarSubset(dataVar, latStart, lonStart, nSubsetLat, nSubsetLon)
	case dim0Len == uint64(nLon) && dim1Len == uint64(nLat):
		// Data is [lon, lat] - need to transpose.
		transposed, err := read2DFloat64VarSubset(dataVar, lonStart, latStart, nSubsetLon, nSubsetLat)
		if err != nil {
			return err
		}
		values = transpose2D(transposed)
	default:
		return fmt.Errorf("dimension mismatch: data is [%d, %d], expected [%d, %d] or [%d, %d]",
			dim0Len, dim1Len, nLat, nLon, nLon, nLat)
	}

	if err != nil {
		return fmt.Errorf("failed to read data: %w", err)
	}

	// Create Grid2D with subset data.
	grid := &interp.Grid2D{
		X:      subsetLon,
		Y:      subsetLat,
		Values: values,
	}

	// Validate grid.
	if err := grid.Validate(); err != nil {
		return fmt.Errorf("invalid grid: %w", err)
	}

	s.grid = grid
	s.bounds = boundsFromGrid(grid)

	return nil
}

// readCoordVar reads a 1D coordinate variable, trying the given names in order.
func readCoordVar(nc netcdf.Dataset, kind string, names []string) ([]float64, error) {
	for _, name := range names {
		if v, err := nc.Var(name); err == nil {
			if data, err := readFloat64Var(v); err == nil {
				return data, nil
			}
		}
	}
	return nil, fmt.Errorf("%s variable not found (tried: %v)", kind, names)
}

// readFloat64Var reads a 1D float64 array from a NetCDF variable.
// Supports DOUBLE, FLOAT, INT, and SHORT variable types.
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

	t, err := v.Type()
	if err != nil {
		return nil, fmt.Errorf("failed to get var type: %w", err)
	}

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

// Close releases resources.
func (s *Store) Close() error {
	return nil
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
		return nil, fmt.Errorf("unsupported data type: %v", varType)
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
