// Package store defines interfaces for loading tidal constituent data.
package store

import (
	"errors"

	"go.ngs.io/tides-api/internal/domain"
)

// ErrNoData marks a location that is simply not covered by the model data -
// typically a land cell whose grid neighbours are all fill values. It is a
// data-coverage condition, not an internal failure: callers map it to a 404
// rather than a 500. Genuine internal problems (missing NetCDF files, read
// errors) must NOT be wrapped with it.
var ErrNoData = errors.New("no data available for location")

// ConstituentLoader is the interface for loading tidal constituent parameters.
type ConstituentLoader interface {
	// LoadForStation loads parameters for a named station (e.g., "tokyo").
	LoadForStation(stationID string) ([]domain.ConstituentParam, error)

	// LoadForLocation loads parameters for a lat/lon location (using interpolation for FES).
	LoadForLocation(lat, lon float64) ([]domain.ConstituentParam, error)
}
