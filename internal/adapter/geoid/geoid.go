// Package geoid provides access to EGM2008 geoid data for MSL corrections.
package geoid

import (
	"fmt"
	"math"
	"sync"

	"github.com/fhs/go-netcdf/netcdf"

	"go.ngs.io/tides-api/internal/adapter/interp"
)

// Store provides geoid height lookups for coordinate transformations.
type Store struct {
	geoidPath string // Path to EGM2008 NetCDF file.
	grid      *interp.Grid2D
	mu        sync.RWMutex
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

	// Load grid on first access.
	if s.grid == nil {
		if err := s.loadGrid(lat, lon); err != nil {
			return 0, fmt.Errorf("failed to load geoid grid: %w", err)
		}
	}

	// Interpolate geoid height.
	height, err := s.grid.InterpolateAt(lon, lat)
	if err != nil {
		return 0, fmt.Errorf("failed to interpolate geoid height: %w", err)
	}

	return height, nil
}

// loadGrid loads a subset of the EGM2008 NetCDF grid around the target location.
func (s *Store) loadGrid(targetLat, targetLon float64) error {
	nc, err := netcdf.OpenFile(s.geoidPath, netcdf.NOWRITE)
	if err != nil {
		return fmt.Errorf("failed to open NetCDF file: %w", err)
	}
	defer nc.Close()

	// Try common variable names for geoid grids.
	latNames := []string{"lat", "latitude", "y"}
	lonNames := []string{"lon", "longitude", "x"}
	dataNames := []string{"geoid", "geoid_height", "N", "height", "z"}

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
		return fmt.Errorf("latitude variable not found (tried: %v)", latNames)
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
		return fmt.Errorf("longitude variable not found (tried: %v)", lonNames)
	}

	// Calculate subset indices with ±2 degree margin.
	const margin = 2.0 // Degrees.
	latStartIdx := findNearestIndex(latData, targetLat-margin)
	latEndIdx := findNearestIndex(latData, targetLat+margin)
	lonStartIdx := findNearestIndex(lonData, targetLon-margin)
	lonEndIdx := findNearestIndex(lonData, targetLon+margin)

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
	s.grid = &interp.Grid2D{
		X:      subsetLon,
		Y:      subsetLat,
		Values: values,
	}

	// Validate grid.
	if err := s.grid.Validate(); err != nil {
		return fmt.Errorf("invalid grid: %w", err)
	}

	return nil
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
	start := []uint64{uint64(startRow), uint64(startCol)}
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

	// Apply scale_factor if present.
	scaleAttr := v.Attr("scale_factor")
	attrLen, err := scaleAttr.Len()
	if err == nil && attrLen > 0 {
		var scaleVal float64
		scaleData := make([]float64, 1)
		err = scaleAttr.ReadFloat64s(scaleData)
		if err == nil {
			scaleVal = scaleData[0]
		} else {
			int32Data := make([]int32, 1)
			err = scaleAttr.ReadInt32s(int32Data)
			if err == nil {
				scaleVal = float64(int32Data[0])
			}
		}
		if err == nil && scaleVal != 0 {
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

// clamp ensures value is within [min, max] range.
func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
