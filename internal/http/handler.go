// Package http provides HTTP handlers for the tides API.
package http

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"go.ngs.io/tides-api/internal/domain"
	"go.ngs.io/tides-api/internal/usecase"
)

// stationIDPattern restricts station IDs to alphanumerics, hyphens and
// underscores so they can never be used for path traversal when resolved
// against the data directory.
var stationIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Query parameter / response field names shared across handlers.
const (
	paramLat   = "lat"
	paramLon   = "lon"
	paramStart = "start"
	paramEnd   = "end"
)

// errorJSON builds the standard error response body.
func errorJSON(msg string) gin.H {
	return gin.H{"error": msg}
}

// Handler handles HTTP requests for tide predictions.
type Handler struct {
	predictionUC *usecase.PredictionUseCase
}

// NewHandler creates a new HTTP handler.
func NewHandler(predictionUC *usecase.PredictionUseCase) *Handler {
	return &Handler{
		predictionUC: predictionUC,
	}
}

// GetPredictions handles GET /v1/tides/predictions.
func (h *Handler) GetPredictions(c *gin.Context) {
	req := usecase.PredictionRequest{
		Datum:           c.Query("datum"),
		Source:          c.Query("source"),
		Timezone:        c.Query("timezone"),         // "utc" (default), "jst", or an IANA name (e.g. "Asia/Tokyo").
		PhaseConvention: c.Query("phase_convention"), // "fes_greenwich" (default) or "vu"
	}

	if !parseLocation(c, &req) || !parseTimeRange(c, &req) || !parseIntervalAndOffset(c, &req) {
		return
	}

	// Execute the use case and map typed errors to status codes. Validation
	// (including source/identifier combination checks) happens inside
	// Execute; ErrValidation / ErrNotFound messages are guaranteed by the
	// use case to be safe to expose. Anything else is an internal error and
	// must not leak details such as file paths to the client.
	response, err := h.predictionUC.Execute(req)
	if err != nil {
		switch {
		case errors.Is(err, usecase.ErrValidation):
			c.JSON(http.StatusBadRequest, errorJSON(err.Error()))
		case errors.Is(err, usecase.ErrNotFound):
			c.JSON(http.StatusNotFound, errorJSON(err.Error()))
		default:
			log.Printf("prediction execute failed: %v", err)
			c.JSON(http.StatusInternalServerError, errorJSON("internal server error"))
		}
		return
	}

	c.JSON(http.StatusOK, response)
}

// parseLocation fills lat/lon and station_id on req. It writes a 400 response
// and returns false on invalid input.
func parseLocation(c *gin.Context, req *usecase.PredictionRequest) bool {
	latStr := c.Query(paramLat)
	lonStr := c.Query(paramLon)
	stationID := c.Query("station_id")

	// Reject partial lat/lon pairs explicitly instead of silently ignoring them.
	if latStr != "" && lonStr == "" {
		c.JSON(http.StatusBadRequest, errorJSON("latitude and longitude must both be provided (lon is missing)"))
		return false
	}
	if lonStr != "" && latStr == "" {
		c.JSON(http.StatusBadRequest, errorJSON("latitude and longitude must both be provided (lat is missing)"))
		return false
	}

	if latStr != "" && lonStr != "" {
		lat, err := strconv.ParseFloat(latStr, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, errorJSON(fmt.Sprintf("invalid latitude: %v", err)))
			return false
		}
		lon, err := strconv.ParseFloat(lonStr, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, errorJSON(fmt.Sprintf("invalid longitude: %v", err)))
			return false
		}
		req.Lat = &lat
		req.Lon = &lon
	}

	// Validate station ID before it can ever reach file access.
	if stationID != "" {
		if !stationIDPattern.MatchString(stationID) {
			c.JSON(http.StatusBadRequest, errorJSON("invalid station_id: only alphanumeric characters, hyphens and underscores are allowed"))
			return false
		}
		req.StationID = &stationID
	}
	return true
}

// parseTimeRange fills Start/End (and a coordinate-derived Timezone) on req.
// It writes a 400 response and returns false on invalid input.
func parseTimeRange(c *gin.Context, req *usecase.PredictionRequest) bool {
	startStr := c.Query(paramStart)
	endStr := c.Query(paramEnd)

	// If missing and lat/lon provided, default to local (resolved) current day 00:00-24:00.
	if startStr == "" && endStr == "" && req.Lat != nil && req.Lon != nil {
		// Resolve simple timezone: JST for Japan bounding box, otherwise UTC.
		loc, tzCode := resolveTimezoneForLatLon(*req.Lat, *req.Lon)
		if req.Timezone == "" {
			req.Timezone = tzCode
		}
		nowLocal := time.Now().In(loc)
		y, m, d := nowLocal.Date()
		startLocal := time.Date(y, m, d, 0, 0, 0, 0, loc)
		req.Start = startLocal.UTC()
		req.End = startLocal.Add(24 * time.Hour).UTC()
		return true
	}

	if startStr == "" {
		c.JSON(http.StatusBadRequest, errorJSON("start parameter is required"))
		return false
	}
	if endStr == "" {
		c.JSON(http.StatusBadRequest, errorJSON("end parameter is required"))
		return false
	}
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorJSON(fmt.Sprintf("invalid start time (expected RFC3339): %v", err)))
		return false
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorJSON(fmt.Sprintf("invalid end time (expected RFC3339): %v", err)))
		return false
	}
	req.Start = start.UTC()
	req.End = end.UTC()

	// If timezone not provided but lat/lon present, set output TZ based on coordinates (always-on).
	if req.Timezone == "" && req.Lat != nil && req.Lon != nil {
		_, tzCode := resolveTimezoneForLatLon(*req.Lat, *req.Lon)
		req.Timezone = tzCode
	}
	return true
}

// parseIntervalAndOffset fills Interval and DatumOffsetM on req. It writes a
// 400 response and returns false on invalid input.
func parseIntervalAndOffset(c *gin.Context, req *usecase.PredictionRequest) bool {
	// Parse interval (default: 30m for better readability).
	intervalStr := c.Query("interval")
	if intervalStr == "" {
		intervalStr = "30m"
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorJSON(fmt.Sprintf("invalid interval: %v", err)))
		return false
	}
	req.Interval = interval

	if datumOffsetStr := c.Query("datum_offset_m"); datumOffsetStr != "" {
		off, err := strconv.ParseFloat(datumOffsetStr, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, errorJSON(fmt.Sprintf("invalid datum_offset_m: %v", err)))
			return false
		}
		req.DatumOffsetM = &off
	}
	return true
}

// resolveTimezoneForLatLon returns a best-effort location and label based on lat/lon.
// Currently: Japan bounding box -> JST (+09:00), otherwise UTC.
func resolveTimezoneForLatLon(lat, lon float64) (*time.Location, string) {
	// Rough Japan bounding box (includes main islands): 20–46N, 122–154E
	if lat >= 20 && lat <= 46 && lon >= 122 && lon <= 154 {
		return time.FixedZone("JST", 9*60*60), "jst"
	}
	return time.FixedZone("UTC", 0), "utc"
}

// HealthCheck handles GET /health.
func (h *Handler) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// ConstituentListResponse is the response for listing constituents.
type ConstituentListResponse struct {
	Name          string  `json:"name"`
	SpeedDegPerHr float64 `json:"speed_deg_per_hr"`
	Description   string  `json:"description,omitempty"`
}

// GetConstituentsList returns a detailed list of all constituents.
func (h *Handler) GetConstituentsList(c *gin.Context) {
	constituents := domain.GetAllConstituents()

	// Add descriptions for major constituents.
	descriptions := map[string]string{
		"M2":  "Principal lunar semidiurnal",
		"S2":  "Principal solar semidiurnal",
		"N2":  "Larger lunar elliptic semidiurnal",
		"K2":  "Lunisolar semidiurnal",
		"K1":  "Lunar diurnal",
		"O1":  "Lunar diurnal",
		"P1":  "Solar diurnal",
		"Q1":  "Solar diurnal",
		"M4":  "Shallow water overtide of M2",
		"M6":  "Shallow water overtide of M2",
		"MK3": "Shallow water terdiurnal",
		"S4":  "Shallow water overtide of S2",
		"MN4": "Shallow water quarter diurnal",
		"MS4": "Shallow water quarter diurnal",
		"Mf":  "Lunisolar fortnightly",
		"Mm":  "Lunar monthly",
		"Ssa": "Solar semiannual",
		"Sa":  "Solar annual",
	}

	response := make([]ConstituentListResponse, len(constituents))
	for i, c := range constituents {
		response[i] = ConstituentListResponse{
			Name:          c.Name,
			SpeedDegPerHr: c.SpeedDegPerHr,
			Description:   descriptions[c.Name],
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"constituents": response,
		"count":        len(response),
	})
}

// GetBathymetry handles GET /v1/bathymetry.
func (h *Handler) GetBathymetry(c *gin.Context) {
	// Parse query parameters.
	latStr := c.Query(paramLat)
	lonStr := c.Query(paramLon)

	if latStr == "" || lonStr == "" {
		c.JSON(http.StatusBadRequest, errorJSON("lat and lon parameters are required"))
		return
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorJSON(fmt.Sprintf("invalid latitude: %v", err)))
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorJSON(fmt.Sprintf("invalid longitude: %v", err)))
		return
	}

	// Validate ranges.
	if lat < -90 || lat > 90 {
		c.JSON(http.StatusBadRequest, errorJSON("latitude must be between -90 and 90"))
		return
	}
	if lon < -180 || lon > 180 {
		c.JSON(http.StatusBadRequest, errorJSON("longitude must be between -180 and 180"))
		return
	}

	// Get bathymetry data.
	metadata, err := h.predictionUC.GetBathymetry(lat, lon)
	if err != nil {
		c.JSON(http.StatusNotFound, errorJSON(err.Error()))
		return
	}

	// Build response.
	response := gin.H{
		"location": gin.H{
			paramLat: lat,
			paramLon: lon,
		},
		"msl_m":      metadata.MSL,
		"datum_name": metadata.DatumName,
		"source":     metadata.SourceName,
	}

	if metadata.DepthM != nil {
		response["depth_m"] = *metadata.DepthM
	}

	c.JSON(http.StatusOK, response)
}
