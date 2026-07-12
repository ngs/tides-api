// Package usecase contains business logic for tide predictions.
package usecase

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"time"

	"go.ngs.io/tides-api/internal/adapter/store"
	"go.ngs.io/tides-api/internal/adapter/store/bathymetry"
	"go.ngs.io/tides-api/internal/domain"
)

const (
	sourceCSV = "csv"
	sourceFES = "fes"
)

// Sentinel errors that let transport layers (HTTP handlers) map failures to
// the right status code via errors.Is without duplicating business rules.
var (
	// ErrValidation marks client-caused request errors (HTTP 400). Messages
	// wrapped with ErrValidation are safe to expose to clients and must never
	// contain internal details such as file paths.
	ErrValidation = errors.New("invalid request")

	// ErrNotFound marks requests referencing data that does not exist, such
	// as an unknown station (HTTP 404). Messages wrapped with ErrNotFound are
	// safe to expose to clients.
	ErrNotFound = errors.New("not found")
)

// PredictionRequest encapsulates a tide prediction request.
type PredictionRequest struct {
	// Location parameters (mutually exclusive with StationID).
	Lat *float64
	Lon *float64

	// Station ID (mutually exclusive with Lat/Lon).
	StationID *string

	// Time range.
	Start time.Time
	End   time.Time

	// Interval for predictions (e.g., 10 minutes).
	Interval time.Duration

	// Optional parameters.
	Datum  string // E.g., "MSL", "LAT", "MLLW" - MVP uses MSL only.
	Source string // "csv" or "fes" - if empty, auto-detect.

	// Optional vertical datum offset in meters to adjust heights for comparison with external datums
	// (e.g., JMA's DL/TP). Positive values raise all predicted heights by the given amount.
	DatumOffsetM *float64

	// Output timezone preference for formatted timestamps in the response.
	// Supported: "utc" (default), "jst".
	Timezone string

	// Optional phase convention selector: "fes_greenwich" (default) or "vu".
	PhaseConvention string
}

// PredictionResponse contains the tide prediction results.
type PredictionResponse struct {
	Source       string            `json:"source"`
	Datum        string            `json:"datum"`
	Timezone     string            `json:"timezone"`
	Constituents []string          `json:"constituents"`
	Predictions  []PredictionPoint `json:"predictions"`
	Extrema      ExtremaResponse   `json:"extrema"`
	MSL          *float64          `json:"msl_m,omitempty"`          // Mean Sea Level in meters.
	SeabedDepth  *float64          `json:"seabed_depth_m,omitempty"` // Seabed depth in meters (positive value).
	Meta         map[string]string `json:"meta"`
}

// PredictionPoint represents a single tide height prediction.
type PredictionPoint struct {
	Time    string   `json:"time"`
	HeightM float64  `json:"height_m"`          // Tide height relative to datum.
	DepthM  *float64 `json:"depth_m,omitempty"` // Water depth at this time (seabed_depth + height; height already includes MSL).
}

// ExtremaResponse contains high and low tides.
type ExtremaResponse struct {
	Highs []PredictionPoint `json:"highs"`
	Lows  []PredictionPoint `json:"lows"`
}

// PredictionUseCase orchestrates tide prediction.
type PredictionUseCase struct {
	csvStore        *store.ConstituentLoader
	fesStore        *store.ConstituentLoader
	bathymetryStore bathymetry.Store // Optional bathymetry/MSL data store.
}

// NewPredictionUseCase creates a new prediction use case.
func NewPredictionUseCase(csvStore, fesStore store.ConstituentLoader, bathyStore bathymetry.Store) *PredictionUseCase {
	return &PredictionUseCase{
		csvStore:        &csvStore,
		fesStore:        &fesStore,
		bathymetryStore: bathyStore,
	}
}

// Validate checks if the request is valid.
func (r *PredictionRequest) Validate() error {
	// Check mutually exclusive parameters.
	hasLatLon := r.Lat != nil && r.Lon != nil
	hasStationID := r.StationID != nil && *r.StationID != ""

	if !hasLatLon && !hasStationID {
		return fmt.Errorf("either lat/lon or station_id must be provided")
	}

	if hasLatLon && hasStationID {
		return fmt.Errorf("lat/lon and station_id are mutually exclusive")
	}

	// Validate lat/lon ranges.
	if hasLatLon {
		if *r.Lat < -90 || *r.Lat > 90 {
			return fmt.Errorf("latitude must be between -90 and 90")
		}
		if *r.Lon < -180 || *r.Lon > 180 {
			return fmt.Errorf("longitude must be between -180 and 180")
		}
	}

	// Validate time range.
	if !r.Start.Before(r.End) {
		return fmt.Errorf("start time must be before end time")
	}

	// Validate interval.
	if r.Interval < time.Minute {
		return fmt.Errorf("interval must be at least 1 minute")
	}
	if r.Interval > 6*time.Hour {
		return fmt.Errorf("interval must be at most 6 hours")
	}

	// Check that time range is reasonable.
	duration := r.End.Sub(r.Start)
	if duration > 365*24*time.Hour {
		return fmt.Errorf("time range must be at most 365 days")
	}

	// Check that number of points is reasonable.
	numPoints := int(duration / r.Interval)
	if numPoints > 10000 {
		return fmt.Errorf("too many prediction points (%d) - reduce time range or increase interval", numPoints)
	}

	return nil
}

// Execute performs the tide prediction.
//
//nolint:gocyclo,nestif // Complex prediction logic with multiple conditional paths.
func (uc *PredictionUseCase) Execute(req PredictionRequest) (*PredictionResponse, error) {
	// Validate request.
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	// Determine source and load constituents.
	var constituents []domain.ConstituentParam
	var source string
	var err error

	// Match Validate: a pointer to an empty StationID is treated as absent.
	if req.StationID != nil && *req.StationID != "" {
		// Use CSV store for station-based queries.
		source = sourceCSV
		if req.Source == sourceFES {
			return nil, fmt.Errorf("%w: FES source does not support station_id - use lat/lon instead", ErrValidation)
		}
		constituents, err = (*uc.csvStore).LoadForStation(*req.StationID)
		if err != nil {
			// A missing data file means the station does not exist. Do not
			// wrap the underlying store error, which may contain file paths.
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("%w: no data for station %q", ErrNotFound, *req.StationID)
			}
			return nil, fmt.Errorf("failed to load constituents for station %s: %w", *req.StationID, err)
		}
	} else {
		// Use FES store for lat/lon queries (or CSV if explicitly requested).
		if req.Source == sourceCSV {
			return nil, fmt.Errorf("%w: CSV source does not support lat/lon - use station_id instead", ErrValidation)
		}
		source = sourceFES
		constituents, err = (*uc.fesStore).LoadForLocation(*req.Lat, *req.Lon)
		if err != nil {
			return nil, fmt.Errorf("failed to load constituents for location (%.4f, %.4f): %w", *req.Lat, *req.Lon, err)
		}
	}

	// Load bathymetry metadata if available (lat/lon queries only).
	var metadata *domain.LocationMetadata
	if req.Lat != nil && req.Lon != nil && uc.bathymetryStore != nil {
		var err error
		metadata, err = uc.bathymetryStore.GetMetadata(*req.Lat, *req.Lon)
		if err != nil {
			// Metadata is optional - log warning but continue.
			log.Printf("Warning: failed to load bathymetry metadata: %v", err)
		}
	}

	// Set up prediction parameters.
	msl := 0.0
	if metadata != nil {
		msl = metadata.MSL
	}

	// Apply optional datum offset (e.g., to align with JMA DL/TP).
	if req.DatumOffsetM != nil {
		msl += *req.DatumOffsetM
	} else if req.Lat != nil && req.Lon != nil {
		// Auto datum offset: attempt to load nearest known offset (e.g., JMA DL/TP) and apply.
		if off, ok := getAutoDatumOffset(*req.Lat, *req.Lon); ok {
			msl += off
		}
	}

	if req.Lat != nil && req.Lon != nil {
		constituents = applyStationOverride(*req.Lat, *req.Lon, constituents, &msl)
	}

	// Set longitude for Greenwich phase correction (only for lat/lon queries).
	lon := 0.0
	if req.Lon != nil {
		lon = *req.Lon
	}

	// Choose prediction phase convention.
	var phaseConv domain.PhaseConvention
	switch req.PhaseConvention {
	case "vu", "VU":
		phaseConv = domain.PhaseConvVu
	default:
		phaseConv = domain.PhaseConvFESGreenwich
	}

	// Reference time: use FES epoch for FES source to align phases, else Unix epoch.
	refTime := time.Unix(0, 0).UTC()
	if source == sourceFES {
		// FES2014 phases are commonly referenced to 2012-01-01 00:00:00 UTC.
		refTime = time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	params := domain.PredictionParams{
		Constituents:    constituents,
		MSL:             msl,
		Longitude:       lon,
		NodalCorrection: domain.NewAstronomicalNodalCorrection(),
		ReferenceTime:   refTime,
		PhaseConvention: phaseConv,
	}

	// Resolve output timezone before heavy computation so invalid values fail fast.
	loc, tzLabel, err := resolveTimezone(req.Timezone, req.Start)
	if err != nil {
		return nil, err
	}

	// Generate predictions at requested interval.
	predictions := domain.GeneratePredictions(req.Start, req.End, req.Interval, params)

	// Compute extrema on high-resolution (1m) grid for accurate times regardless of interval.
	// Cap the total number of high-resolution points (60 days at 1-minute resolution)
	// to bound CPU cost for long time ranges; coarsen the grid beyond that.
	const maxPrecisePoints = 86400
	preciseInterval := time.Minute
	if req.Interval < preciseInterval {
		preciseInterval = req.Interval
	}
	if n := req.End.Sub(req.Start) / preciseInterval; n > maxPrecisePoints {
		preciseInterval = req.End.Sub(req.Start) / maxPrecisePoints
		// Keep the grid aligned to whole minutes.
		if rem := preciseInterval % time.Minute; rem != 0 {
			preciseInterval += time.Minute - rem
		}
	}
	precisePredictions := domain.GeneratePredictions(req.Start, req.End, preciseInterval, params)
	extrema := domain.RefineExtrema(precisePredictions, domain.FindExtrema(precisePredictions))

	// Convert to response format.
	predictionPoints := make([]PredictionPoint, len(predictions))
	for i, p := range predictions {
		point := PredictionPoint{
			Time:    p.Time.In(loc).Format(time.RFC3339),
			HeightM: roundToDecimal(p.HeightM),
		}

		// Calculate water depth if seabed depth is available.
		// Water depth = seabed_depth + tide_height (HeightM already includes MSL).
		if metadata != nil && metadata.DepthM != nil {
			waterDepth := *metadata.DepthM + p.HeightM
			roundedDepth := roundToDecimal(waterDepth)
			point.DepthM = &roundedDepth
		}

		predictionPoints[i] = point
	}

	highPoints := make([]PredictionPoint, len(extrema.Highs))
	for i, h := range extrema.Highs {
		point := PredictionPoint{
			Time:    h.Time.In(loc).Format(time.RFC3339),
			HeightM: roundToDecimal(h.HeightM),
		}

		// Calculate water depth if seabed depth is available.
		if metadata != nil && metadata.DepthM != nil {
			waterDepth := *metadata.DepthM + h.HeightM
			roundedDepth := roundToDecimal(waterDepth)
			point.DepthM = &roundedDepth
		}

		highPoints[i] = point
	}

	lowPoints := make([]PredictionPoint, len(extrema.Lows))
	for i, l := range extrema.Lows {
		point := PredictionPoint{
			Time:    l.Time.In(loc).Format(time.RFC3339),
			HeightM: roundToDecimal(l.HeightM),
		}

		// Calculate water depth if seabed depth is available.
		if metadata != nil && metadata.DepthM != nil {
			waterDepth := *metadata.DepthM + l.HeightM
			roundedDepth := roundToDecimal(waterDepth)
			point.DepthM = &roundedDepth
		}

		lowPoints[i] = point
	}

	// Extract constituent names.
	constituentNames := make([]string, len(constituents))
	for i, c := range constituents {
		constituentNames[i] = c.Name
	}

	// Determine datum.
	datum := req.Datum
	if datum == "" {
		datum = "MSL"
	}

	// Build response.
	response := &PredictionResponse{
		Source:       source,
		Datum:        datum,
		Timezone:     tzLabel,
		Constituents: constituentNames,
		Predictions:  predictionPoints,
		Extrema: ExtremaResponse{
			Highs: highPoints,
			Lows:  lowPoints,
		},
		Meta: map[string]string{
			"model": "harmonic_v0",
		},
	}

	// Add metadata if available.
	if metadata != nil {
		if metadata.MSL != 0.0 {
			response.MSL = &metadata.MSL
		}
		if metadata.DepthM != nil {
			response.SeabedDepth = metadata.DepthM
		}
		if metadata.DatumName != "" {
			response.Meta["datum_name"] = metadata.DatumName
		}
		if metadata.SourceName != "" {
			response.Meta["metadata_source"] = metadata.SourceName
		}
	}

	// Add attribution based on source.
	if source == sourceCSV {
		response.Meta["attribution"] = "Mock CSV (for dev). Replace with FES later."
	} else {
		response.Meta["attribution"] = "FES2014/2022 tidal model"
	}

	// Record applied datum offset if provided.
	if req.DatumOffsetM != nil {
		response.Meta["datum_offset_m"] = fmt.Sprintf("%.3f", *req.DatumOffsetM)
	}

	return response, nil
}

// GetAllConstituents returns all available constituents.
func (uc *PredictionUseCase) GetAllConstituents() []domain.Constituent {
	return domain.GetAllConstituents()
}

// GetBathymetry returns bathymetry and MSL data for a location.
func (uc *PredictionUseCase) GetBathymetry(lat, lon float64) (*domain.LocationMetadata, error) {
	if uc.bathymetryStore == nil {
		return nil, fmt.Errorf("bathymetry data not available")
	}

	metadata, err := uc.bathymetryStore.GetMetadata(lat, lon)
	if err != nil {
		return nil, fmt.Errorf("failed to get bathymetry data: %w", err)
	}

	if metadata == nil {
		return nil, fmt.Errorf("no bathymetry data available for location (%.4f, %.4f)", lat, lon)
	}

	return metadata, nil
}

// Helper function to round to 3 decimal places (half away from zero).
func roundToDecimal(val float64) float64 {
	const multiplier = 1000.0
	return math.Round(val*multiplier) / multiplier
}

// resolveTimezone maps a requested timezone string to a *time.Location and an
// offset label. An empty string defaults to UTC; unsupported values are an error.
func resolveTimezone(tz string, at time.Time) (*time.Location, string, error) {
	switch tz {
	case "", "utc", "UTC":
		return time.UTC, "+00:00", nil
	case "jst", "JST":
		return time.FixedZone("JST", 9*60*60), "+09:00", nil
	default:
		loc, err := time.LoadLocation(tz)
		if err != nil {
			return nil, "", fmt.Errorf("%w: unsupported timezone %q", ErrValidation, tz)
		}
		return loc, at.In(loc).Format("-07:00"), nil
	}
}
