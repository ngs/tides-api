package store

import "tide-api/internal/domain"

// ConstituentLoader is the interface for loading tidal constituent parameters
type ConstituentLoader interface {
	// LoadForStation loads parameters for a named station (e.g., "tokyo")
	LoadForStation(stationID string) ([]domain.ConstituentParam, error)

	// LoadForLocation loads parameters for a lat/lon location (using interpolation for FES)
	LoadForLocation(lat, lon float64) ([]domain.ConstituentParam, error)
}
