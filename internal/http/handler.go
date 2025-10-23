package http

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"go.ngs.io/tides-api/internal/domain"
	"go.ngs.io/tides-api/internal/usecase"
)

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

	// Build request.
	req := usecase.PredictionRequest{
		Datum:  datum,
		Source: source,
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

	// Parse station ID.
	if stationID != "" {
		req.StationID = &stationID
	}

	// Parse time range.
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

	// Parse interval (default: 10m).
	if intervalStr == "" {
		intervalStr = "10m"
	}

	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid interval: %v", err)})
		return
	}
	req.Interval = interval

	// Execute use case.
	response, err := h.predictionUC.Execute(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetConstituents handles GET /v1/constituents.
func (h *Handler) GetConstituents(c *gin.Context) {
	constituents := h.predictionUC.GetAllConstituents()

	// Convert to response format.
	type ConstituentInfo struct {
		Name          string  `json:"name"`
		SpeedDegPerHr float64 `json:"speed_deg_per_hr"`
	}

	response := make([]ConstituentInfo, len(constituents))
	for i, c := range constituents {
		response[i] = ConstituentInfo{
			Name:          c.Name,
			SpeedDegPerHr: c.SpeedDegPerHr,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"constituents": response,
	})
}

// HealthCheck handles GET /healthz.
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
