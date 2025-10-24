package bathymetry

import "go.ngs.io/tides-api/internal/domain"

// Store provides access to bathymetry (depth) and mean sea level data.
type Store interface {
	// GetMetadata loads location metadata (MSL and depth) for a lat/lon location.
	// Returns nil if data is not available for the location.
	GetMetadata(lat, lon float64) (*domain.LocationMetadata, error)

	// Close releases any resources held by the store.
	Close() error
}
