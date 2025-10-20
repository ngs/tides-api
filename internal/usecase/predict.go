package usecase

import (
	"fmt"
	"time"

	"tide-api/internal/adapter/store"
	"tide-api/internal/domain"
)

// PredictionRequest encapsulates a tide prediction request
type PredictionRequest struct {
	// Location parameters (mutually exclusive with StationID)
	Lat *float64
	Lon *float64

	// Station ID (mutually exclusive with Lat/Lon)
	StationID *string

	// Time range
	Start time.Time
	End   time.Time

	// Interval for predictions (e.g., 10 minutes)
	Interval time.Duration

	// Optional parameters
	Datum  string // e.g., "MSL", "LAT", "MLLW" - MVP uses MSL only
	Source string // "csv" or "fes" - if empty, auto-detect
}

// PredictionResponse contains the tide prediction results
type PredictionResponse struct {
	Source       string                    `json:"source"`
	Datum        string                    `json:"datum"`
	Timezone     string                    `json:"timezone"`
	Constituents []string                  `json:"constituents"`
	Predictions  []PredictionPoint         `json:"predictions"`
	Extrema      ExtremaResponse           `json:"extrema"`
	Meta         map[string]string         `json:"meta"`
}

// PredictionPoint represents a single tide height prediction
type PredictionPoint struct {
	Time     string  `json:"time"`
	HeightM  float64 `json:"height_m"`
}

// ExtremaResponse contains high and low tides
type ExtremaResponse struct {
	Highs []PredictionPoint `json:"highs"`
	Lows  []PredictionPoint `json:"lows"`
}

// PredictionUseCase orchestrates tide prediction
type PredictionUseCase struct {
	csvStore *store.ConstituentLoader
	fesStore *store.ConstituentLoader
}

// NewPredictionUseCase creates a new prediction use case
func NewPredictionUseCase(csvStore, fesStore store.ConstituentLoader) *PredictionUseCase {
	return &PredictionUseCase{
		csvStore: &csvStore,
		fesStore: &fesStore,
	}
}

// Validate checks if the request is valid
func (r *PredictionRequest) Validate() error {
	// Check mutually exclusive parameters
	hasLatLon := r.Lat != nil && r.Lon != nil
	hasStationID := r.StationID != nil && *r.StationID != ""

	if !hasLatLon && !hasStationID {
		return fmt.Errorf("either lat/lon or station_id must be provided")
	}

	if hasLatLon && hasStationID {
		return fmt.Errorf("lat/lon and station_id are mutually exclusive")
	}

	// Validate lat/lon ranges
	if hasLatLon {
		if *r.Lat < -90 || *r.Lat > 90 {
			return fmt.Errorf("latitude must be between -90 and 90")
		}
		if *r.Lon < -180 || *r.Lon > 180 {
			return fmt.Errorf("longitude must be between -180 and 180")
		}
	}

	// Validate time range
	if !r.Start.Before(r.End) {
		return fmt.Errorf("start time must be before end time")
	}

	// Validate interval
	if r.Interval < time.Minute {
		return fmt.Errorf("interval must be at least 1 minute")
	}
	if r.Interval > 6*time.Hour {
		return fmt.Errorf("interval must be at most 6 hours")
	}

	// Check that time range is reasonable
	duration := r.End.Sub(r.Start)
	if duration > 365*24*time.Hour {
		return fmt.Errorf("time range must be at most 365 days")
	}

	// Check that number of points is reasonable
	numPoints := int(duration / r.Interval)
	if numPoints > 10000 {
		return fmt.Errorf("too many prediction points (%d) - reduce time range or increase interval", numPoints)
	}

	return nil
}

// Execute performs the tide prediction
func (uc *PredictionUseCase) Execute(req PredictionRequest) (*PredictionResponse, error) {
	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid request: %w", err)
	}

	// Determine source and load constituents
	var constituents []domain.ConstituentParam
	var source string
	var err error

	if req.StationID != nil {
		// Use CSV store for station-based queries
		source = "csv"
		if req.Source == "fes" {
			return nil, fmt.Errorf("FES source does not support station_id - use lat/lon instead")
		}
		constituents, err = (*uc.csvStore).LoadForStation(*req.StationID)
		if err != nil {
			return nil, fmt.Errorf("failed to load constituents for station %s: %w", *req.StationID, err)
		}
	} else {
		// Use FES store for lat/lon queries (or CSV if explicitly requested)
		if req.Source == "csv" {
			return nil, fmt.Errorf("CSV source does not support lat/lon - use station_id instead")
		}
		source = "fes"
		constituents, err = (*uc.fesStore).LoadForLocation(*req.Lat, *req.Lon)
		if err != nil {
			return nil, fmt.Errorf("failed to load constituents for location (%.4f, %.4f): %w", *req.Lat, *req.Lon, err)
		}
	}

	// Set up prediction parameters
	params := domain.PredictionParams{
		Constituents:    constituents,
		MSL:             0.0, // MVP: assume MSL = 0
		NodalCorrection: &domain.IdentityNodalCorrection{},
		ReferenceTime:   time.Unix(0, 0).UTC(), // Use Unix epoch as reference
	}

	// Generate predictions
	predictions := domain.GeneratePredictions(req.Start, req.End, req.Interval, params)

	// Find extrema
	extrema := domain.FindExtrema(predictions)

	// Refine extrema with parabolic interpolation
	extrema = domain.RefineExtrema(predictions, extrema)

	// Convert to response format
	predictionPoints := make([]PredictionPoint, len(predictions))
	for i, p := range predictions {
		predictionPoints[i] = PredictionPoint{
			Time:    p.Time.UTC().Format(time.RFC3339),
			HeightM: roundToDecimal(p.HeightM, 3),
		}
	}

	highPoints := make([]PredictionPoint, len(extrema.Highs))
	for i, h := range extrema.Highs {
		highPoints[i] = PredictionPoint{
			Time:    h.Time.UTC().Format(time.RFC3339),
			HeightM: roundToDecimal(h.HeightM, 3),
		}
	}

	lowPoints := make([]PredictionPoint, len(extrema.Lows))
	for i, l := range extrema.Lows {
		lowPoints[i] = PredictionPoint{
			Time:    l.Time.UTC().Format(time.RFC3339),
			HeightM: roundToDecimal(l.HeightM, 3),
		}
	}

	// Extract constituent names
	constituentNames := make([]string, len(constituents))
	for i, c := range constituents {
		constituentNames[i] = c.Name
	}

	// Determine datum
	datum := req.Datum
	if datum == "" {
		datum = "MSL"
	}

	// Build response
	response := &PredictionResponse{
		Source:       source,
		Datum:        datum,
		Timezone:     "+00:00", // UTC
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

	// Add attribution based on source
	if source == "csv" {
		response.Meta["attribution"] = "Mock CSV (for dev). Replace with FES later."
	} else {
		response.Meta["attribution"] = "FES2014/2022 tidal model"
	}

	return response, nil
}

// GetAllConstituents returns all available constituents
func (uc *PredictionUseCase) GetAllConstituents() []domain.Constituent {
	return domain.GetAllConstituents()
}

// Helper function to round to decimal places
func roundToDecimal(val float64, precision int) float64 {
	multiplier := 1.0
	for i := 0; i < precision; i++ {
		multiplier *= 10
	}
	return float64(int(val*multiplier+0.5)) / multiplier
}
