package fes

import (
	"fmt"

	"tide-api/internal/domain"
)

// FESStore provides access to FES2014/2022 NetCDF tidal constituent data
// This is a stub implementation for future NetCDF integration
type FESStore struct {
	dataDir string
	// TODO: Add NetCDF file handles, grid metadata, etc.
}

// NewFESStore creates a new FES NetCDF store
func NewFESStore(dataDir string) *FESStore {
	return &FESStore{
		dataDir: dataDir,
	}
}

// LoadForLocation loads constituent parameters for a lat/lon location
// using bilinear interpolation from FES NetCDF grids
//
// TODO: Implementation steps:
// 1. Open NetCDF files for each constituent (amplitude and phase grids)
// 2. Read grid metadata (lat/lon arrays, dimensions)
// 3. Use bilinear interpolation (adapter/interp/bilinear.go) to get values at (lat, lon)
// 4. Convert phase from FES convention to prediction convention if needed
// 5. Return ConstituentParam slice with interpolated values
func (s *FESStore) LoadForLocation(lat, lon float64) ([]domain.ConstituentParam, error) {
	// Stub implementation
	return nil, fmt.Errorf("FES NetCDF store not yet implemented - TODO: integrate github.com/fhs/go-netcdf")
}

// LoadForStation is not supported by FES store (only lat/lon queries)
func (s *FESStore) LoadForStation(stationID string) ([]domain.ConstituentParam, error) {
	return nil, fmt.Errorf("FES store does not support station_id queries - use lat/lon parameters")
}

// GetAvailableConstituents returns the list of constituents available in FES data
func (s *FESStore) GetAvailableConstituents() ([]string, error) {
	// TODO: Scan NetCDF files in dataDir and return available constituents
	return nil, fmt.Errorf("FES NetCDF store not yet implemented")
}

// Example future implementation structure:
//
// type FESGrid struct {
//     Amplitude *interp.Grid2D
//     Phase     *interp.Grid2D
// }
//
// func (s *FESStore) loadConstituent(name string) (*FESGrid, error) {
//     // Open NetCDF file: e.g., fes_dir/m2.nc
//     nc, err := netcdf.OpenFile(fmt.Sprintf("%s/%s.nc", s.dataDir, name), netcdf.NOWRITE)
//     if err != nil {
//         return nil, err
//     }
//     defer nc.Close()
//
//     // Read amplitude and phase grids
//     // Extract lat/lon arrays
//     // Construct Grid2D objects
//     // Return FESGrid
// }
