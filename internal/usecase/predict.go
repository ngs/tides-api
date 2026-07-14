// Package usecase contains business logic for tide predictions.
package usecase

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"strings"
	"time"

	"go.ngs.io/tides-api/internal/adapter/store"
	"go.ngs.io/tides-api/internal/adapter/store/bathymetry"
	"go.ngs.io/tides-api/internal/domain"
)

const (
	sourceCSV = "csv"
	sourceFES = "fes"

	// datumMSL is the default vertical datum label in responses. Predicted
	// heights are centred on mean sea level (the harmonic constants have zero
	// mean).
	datumMSL = "MSL"
	// datumCD labels heights expressed relative to chart datum (Z0), obtained by
	// adding chart_datum_offset_m to the MSL-referenced height.
	datumCD = "CD"
)

// chartDatumConstituents are the four constituents summed to estimate the chart
// datum depth Z0 below MSL (the JMA Z0 definition, H_M2 + H_S2 + H_K1 + H_O1)
// when no fitted or tabulated offset is available. This mirrors the
// conventional lowest-astronomical-tide proxy used for nautical chart datums.
//
//nolint:gochecknoglobals // Intentional: constant lookup set for the Z0 sum.
var chartDatumConstituents = map[string]bool{"M2": true, "S2": true, "K1": true, "O1": true}

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
	// MSL is the constant term applied to every predicted height, in meters.
	// The harmonic constants are always MSL-referenced with zero mean, so this
	// is 0 unless an explicit datum_offset_m was requested. Always present;
	// kept for backward compatibility.
	MSL float64 `json:"msl_m"`
	// ChartDatumOffsetM is how far chart datum (Z0) sits below MSL, in meters
	// (non-negative). Add it to an MSL height to get a chart-datum height; this
	// is exactly what datum=CD does server-side.
	ChartDatumOffsetM float64  `json:"chart_datum_offset_m"`
	SeabedDepth       *float64 `json:"seabed_depth_m,omitempty"` // Seabed depth in meters (positive value).
	Meta              map[string]string `json:"meta"`
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

// validateLocation checks the lat/lon vs station_id parameter combination
// shared by predictions and parameters requests. A pointer to an empty
// StationID is treated as absent.
func validateLocation(lat, lon *float64, stationID *string) error {
	// Check mutually exclusive parameters.
	hasLatLon := lat != nil && lon != nil
	hasStationID := stationID != nil && *stationID != ""

	if !hasLatLon && !hasStationID {
		return fmt.Errorf("either lat/lon or station_id must be provided")
	}

	if hasLatLon && hasStationID {
		return fmt.Errorf("lat/lon and station_id are mutually exclusive")
	}

	// Validate lat/lon ranges. Non-finite values must be rejected explicitly:
	// strconv.ParseFloat accepts "NaN"/"Inf", and every comparison against NaN
	// is false, so a bare range check would let them through to compute paths.
	if hasLatLon {
		if math.IsNaN(*lat) || math.IsInf(*lat, 0) {
			return fmt.Errorf("latitude must be a finite number")
		}
		if math.IsNaN(*lon) || math.IsInf(*lon, 0) {
			return fmt.Errorf("longitude must be a finite number")
		}
		if *lat < -90 || *lat > 90 {
			return fmt.Errorf("latitude must be between -90 and 90")
		}
		if *lon < -180 || *lon > 180 {
			return fmt.Errorf("longitude must be between -180 and 180")
		}
	}

	return nil
}

// Validate checks if the request is valid.
func (r *PredictionRequest) Validate() error {
	if err := validateLocation(r.Lat, r.Lon, r.StationID); err != nil {
		return err
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

// resolvedParams bundles the location-resolved prediction inputs shared by
// Execute and GetParameters: the constituent set (with station overrides
// applied), the effective MSL term (including override intercepts and datum
// offsets), optional bathymetry metadata, and the phase reference epoch.
type resolvedParams struct {
	source       string
	constituents []domain.ConstituentParam
	metadata     *domain.LocationMetadata
	// msl is the constant term added to the harmonic sum. It is 0 for the
	// MSL-referenced prediction and carries only an explicit request
	// DatumOffsetM when one is provided.
	msl float64
	// chartDatumOffset is the non-negative depth of chart datum (Z0) below MSL.
	// It comes from the matching station override intercept, an auto datum
	// offset, or the Σ(H_M2+H_S2+H_K1+H_O1) fallback, in that order.
	chartDatumOffset float64
	// mdt is the mean dynamic topography (model MSL above the geoid, e.g. DTU21
	// MSS − EGM2008) reported for information only; it is never added to heights.
	mdt     float64
	refTime time.Time
}

// resolvePredictionParams performs the location-dependent part of a
// prediction: it loads constituents from the appropriate store, fetches
// bathymetry metadata, applies station overrides and datum offsets to the MSL
// term, and determines the phase reference epoch. Only the Lat/Lon/StationID,
// Source and DatumOffsetM fields of req are consulted; location validation is
// the caller's responsibility.
//
//nolint:gocyclo,nestif // Multiple conditional data-source and override paths.
func (uc *PredictionUseCase) resolvePredictionParams(req PredictionRequest) (*resolvedParams, error) {
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
			// The model simply has no data here (typically a land cell): that is
			// a 404, not a server error. Return early - the bathymetry lookups
			// below would fail for the same reason and add nothing.
			if errors.Is(err, store.ErrNoData) {
				return nil, fmt.Errorf("%w: no tide data at (%.4f, %.4f) - the location may be on land", ErrNotFound, *req.Lat, *req.Lon)
			}
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

	// The harmonic constants are always MSL-referenced (zero mean), so the
	// constant term is 0 by default. The model MSL (mean dynamic topography) is
	// reported separately as mdt and never added to heights.
	mdt := 0.0
	if metadata != nil {
		mdt = metadata.MSL
	}

	// A matching station override supplies the fitted constituents; its fitted
	// intercept (mean sea level above the JMA datum, i.e. the chart datum depth
	// below MSL) becomes the chart datum offset rather than a height shift.
	var override *stationOverrideEntry
	if req.Lat != nil && req.Lon != nil {
		override, _ = getStationOverride(*req.Lat, *req.Lon)
	}
	constituents = applyOverrideConstituents(override, constituents)

	// Resolve the chart datum offset (Z0 below MSL), in precedence order:
	//   1. a matching station override's fitted intercept,
	//   2. the nearest tabulated auto datum offset (JMA DL/TP within 80 km),
	//   3. the Σ(H_M2+H_S2+H_K1+H_O1) fallback computed from the constituents.
	// The result is clamped to be non-negative, as chart datum sits at or below
	// MSL by construction.
	var chartDatumOffset float64
	autoOffset, autoOK := 0.0, false
	if req.Lat != nil && req.Lon != nil {
		autoOffset, autoOK = getAutoDatumOffset(*req.Lat, *req.Lon)
	}
	switch {
	case override != nil && override.DatumOffset != nil:
		chartDatumOffset = *override.DatumOffset
	case autoOK:
		chartDatumOffset = autoOffset
	default:
		chartDatumOffset = computeChartDatumOffset(constituents)
	}
	if chartDatumOffset < 0 {
		chartDatumOffset = 0
	}

	// The constant term stays 0 unless the caller asks for an explicit vertical
	// offset (kept for backward compatibility with datum_offset_m).
	msl := 0.0
	if req.DatumOffsetM != nil {
		msl += *req.DatumOffsetM
	}

	// Reference time: use FES epoch for FES source to align phases, else Unix epoch.
	refTime := time.Unix(0, 0).UTC()
	if source == sourceFES {
		// FES2014 phases are commonly referenced to 2012-01-01 00:00:00 UTC.
		refTime = time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	return &resolvedParams{
		source:           source,
		constituents:     constituents,
		metadata:         metadata,
		msl:              msl,
		chartDatumOffset: chartDatumOffset,
		mdt:              mdt,
		refTime:          refTime,
	}, nil
}

// computeChartDatumOffset estimates the chart datum depth below MSL as the sum
// of the four principal constituent amplitudes Σ(H_M2+H_S2+H_K1+H_O1), matching
// the JMA Z0 definition. Constituents not present simply contribute nothing.
func computeChartDatumOffset(constituents []domain.ConstituentParam) float64 {
	var sum float64
	for _, c := range constituents {
		if chartDatumConstituents[c.Name] {
			sum += math.Abs(c.AmplitudeM)
		}
	}
	return sum
}

// Execute performs the tide prediction.
//
//nolint:gocyclo // Complex prediction logic with multiple conditional paths.
func (uc *PredictionUseCase) Execute(req PredictionRequest) (*PredictionResponse, error) {
	// Validate request.
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}

	// Resolve and validate the requested output datum before any heavy work so
	// an unsupported value fails fast with a 400.
	datum, err := resolveDatum(req.Datum)
	if err != nil {
		return nil, err
	}

	resolved, err := uc.resolvePredictionParams(req)
	if err != nil {
		return nil, err
	}
	source := resolved.source
	constituents := resolved.constituents
	metadata := resolved.metadata

	// datum=CD expresses heights relative to chart datum by adding the
	// (non-negative) chart datum offset to every MSL-referenced height. Water
	// depth always uses the MSL height, so the offset is applied only to the
	// reported height_m. Shift by the same rounded value the response reports
	// as chart_datum_offset_m, so height_cd = height_msl + chart_datum_offset_m
	// holds exactly at the API's 3-decimal precision.
	datumShift := 0.0
	if datum == datumCD {
		datumShift = roundToDecimal(resolved.chartDatumOffset)
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

	params := domain.PredictionParams{
		Constituents:    constituents,
		MSL:             resolved.msl,
		Longitude:       lon,
		NodalCorrection: domain.NewAstronomicalNodalCorrection(),
		ReferenceTime:   resolved.refTime,
		PhaseConvention: phaseConv,
	}

	// Resolve output timezone before heavy computation so invalid values fail fast.
	loc, tzLabel, err := resolveTimezone(req.Timezone)
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
	seabedDepth := (*float64)(nil)
	if metadata != nil {
		seabedDepth = metadata.DepthM
	}
	toPoint := func(level domain.TideLevel) PredictionPoint {
		point := PredictionPoint{
			Time:    level.Time.In(loc).Format(time.RFC3339),
			HeightM: roundToDecimal(level.HeightM + datumShift),
		}
		// Water depth = seabed_depth + the unshifted predicted height (which
		// includes an explicit datum_offset_m when one was requested). The
		// chart datum shift (datum=CD) and the mean dynamic topography are
		// never mixed into depth.
		if seabedDepth != nil {
			waterDepth := roundToDecimal(*seabedDepth + level.HeightM)
			point.DepthM = &waterDepth
		}
		return point
	}

	predictionPoints := make([]PredictionPoint, len(predictions))
	for i, p := range predictions {
		predictionPoints[i] = toPoint(p)
	}

	highPoints := make([]PredictionPoint, len(extrema.Highs))
	for i, h := range extrema.Highs {
		highPoints[i] = toPoint(h)
	}

	lowPoints := make([]PredictionPoint, len(extrema.Lows))
	for i, l := range extrema.Lows {
		lowPoints[i] = toPoint(l)
	}

	// Extract constituent names.
	constituentNames := make([]string, len(constituents))
	for i, c := range constituents {
		constituentNames[i] = c.Name
	}

	// Build response. msl_m is the constant term actually applied to heights
	// (0, or an explicit datum_offset_m); the reported datum reflects what was
	// applied (MSL or CD).
	response := &PredictionResponse{
		Source:            source,
		Datum:             datum,
		Timezone:          tzLabel,
		Constituents:      constituentNames,
		Predictions:       predictionPoints,
		ChartDatumOffsetM: roundToDecimal(resolved.chartDatumOffset),
		MSL:               resolved.msl,
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

	// Report the mean dynamic topography (model MSL above the geoid) for
	// information only; it is excluded from heights and depth.
	if resolved.mdt != 0.0 {
		response.Meta["mdt_m"] = fmt.Sprintf("%.3f", resolved.mdt)
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

// resolveDatum normalizes the requested output datum. An empty value defaults
// to MSL. "MSL" and "CD" are accepted case-insensitively; anything else is a
// validation error so the handler returns 400.
func resolveDatum(datum string) (string, error) {
	switch strings.ToUpper(datum) {
	case "", datumMSL:
		return datumMSL, nil
	case datumCD:
		return datumCD, nil
	default:
		return "", fmt.Errorf("%w: unsupported datum %q (supported: MSL, CD)", ErrValidation, datum)
	}
}

// resolveTimezone maps a requested timezone string to a *time.Location and a
// label for the response. An empty string defaults to UTC; unsupported values
// are an error. Fixed zones are labeled with their offset; IANA zones are
// labeled with their identifier, because a single offset would be misleading
// for ranges that cross a DST transition (the per-point RFC3339 timestamps
// carry the actual offsets).
func resolveTimezone(tz string) (*time.Location, string, error) {
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
		return loc, tz, nil
	}
}
