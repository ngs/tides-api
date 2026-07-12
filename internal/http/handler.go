// Package http provides HTTP handlers for the tides API.
package http

import (
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
	// Parse query parameters.
	latStr := c.Query("lat")
	lonStr := c.Query("lon")
	stationID := c.Query("station_id")
	startStr := c.Query("start")
	endStr := c.Query("end")
	intervalStr := c.Query("interval")
	datum := c.Query("datum")
	source := c.Query("source")
	timezone := c.Query("timezone") // "utc" (default) or "jst".
	datumOffsetStr := c.Query("datum_offset_m")
	phaseConv := c.Query("phase_convention") // "fes_greenwich" (default) or "vu"

	// Build request.
	req := usecase.PredictionRequest{
		Datum:    datum,
		Source:   source,
		Timezone: timezone,
	}
	if phaseConv != "" {
		req.PhaseConvention = phaseConv
	}

	// Reject partial lat/lon pairs explicitly instead of silently ignoring them.
	if latStr != "" && lonStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "latitude and longitude must both be provided (lon is missing)"})
		return
	}
	if lonStr != "" && latStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "latitude and longitude must both be provided (lat is missing)"})
		return
	}

	// Parse lat/lon.
	if latStr != "" && lonStr != "" {
		lat, err := strconv.ParseFloat(latStr, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid latitude: %v", err)})
			return
		}
		lon, err := strconv.ParseFloat(lonStr, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid longitude: %v", err)})
			return
		}
		req.Lat = &lat
		req.Lon = &lon
	}

	// Parse station ID. Validate before it can ever reach file access.
	if stationID != "" {
		if !stationIDPattern.MatchString(stationID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid station_id: only alphanumeric characters, hyphens and underscores are allowed"})
			return
		}
		req.StationID = &stationID
	}

	// Parse time range. If missing and lat/lon provided, default to local (resolved) current day 00:00-24:00.
	//nolint:nestif // Time range parsing with multiple default scenarios.
	if startStr == "" && endStr == "" && req.Lat != nil && req.Lon != nil {
		// Resolve simple timezone: JST for Japan bounding box, otherwise UTC.
		loc, tzCode := resolveTimezoneForLatLon(*req.Lat, *req.Lon)
		if timezone == "" {
			req.Timezone = tzCode
		}
		nowLocal := time.Now().In(loc)
		y, m, d := nowLocal.Date()
		startLocal := time.Date(y, m, d, 0, 0, 0, 0, loc)
		endLocal := startLocal.Add(24 * time.Hour)
		req.Start = startLocal.UTC()
		req.End = endLocal.UTC()
	} else {
		if startStr == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "start parameter is required"})
			return
		}
		if endStr == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "end parameter is required"})
			return
		}
		start, err := time.Parse(time.RFC3339, startStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid start time (expected RFC3339): %v", err)})
			return
		}
		end, err := time.Parse(time.RFC3339, endStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid end time (expected RFC3339): %v", err)})
			return
		}
		req.Start = start.UTC()
		req.End = end.UTC()
	}

	// If timezone not provided but lat/lon present, set output TZ based on coordinates (always-on).
	if req.Timezone == "" && req.Lat != nil && req.Lon != nil {
		_, tzCode := resolveTimezoneForLatLon(*req.Lat, *req.Lon)
		req.Timezone = tzCode
	}

	// Parse interval (default: 30m for better readability).
	if intervalStr == "" {
		intervalStr = "30m"
	}

	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid interval: %v", err)})
		return
	}
	req.Interval = interval

	// Parse optional datum offset.
	if datumOffsetStr != "" {
		off, err := strconv.ParseFloat(datumOffsetStr, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid datum_offset_m: %v", err)})
			return
		}
		req.DatumOffsetM = &off
	}

	// Validate the request up front so client errors are reported as 400
	// with a meaningful message. Execute also validates internally, but by
	// validating here we can treat any later Execute failure as internal.
	if err := req.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Source/identifier combination checks mirrored from the use case so
	// they surface as 400 instead of opaque internal errors.
	if req.StationID != nil && req.Source == "fes" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "FES source does not support station_id - use lat/lon instead"})
		return
	}
	if req.Lat != nil && req.Lon != nil && req.Source == "csv" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV source does not support lat/lon - use station_id instead"})
		return
	}

	// Execute use case. The request has already been validated, so any
	// failure here is an internal error (e.g. data store failure). Do not
	// leak internal details such as file paths to the client.
	response, err := h.predictionUC.Execute(req)
	if err != nil {
		log.Printf("prediction execute failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, response)
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
	latStr := c.Query("lat")
	lonStr := c.Query("lon")

	if latStr == "" || lonStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lat and lon parameters are required"})
		return
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid latitude: %v", err)})
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid longitude: %v", err)})
		return
	}

	// Validate ranges.
	if lat < -90 || lat > 90 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "latitude must be between -90 and 90"})
		return
	}
	if lon < -180 || lon > 180 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "longitude must be between -180 and 180"})
		return
	}

	// Get bathymetry data.
	metadata, err := h.predictionUC.GetBathymetry(lat, lon)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Build response.
	response := gin.H{
		"location": gin.H{
			"lat": lat,
			"lon": lon,
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
