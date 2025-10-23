package http

import (
	"os"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"go.ngs.io/tides-api/internal/usecase"
)

// SetupRouter creates and configures the Gin router.
func SetupRouter(predictionUC *usecase.PredictionUseCase) *gin.Engine {

	router := gin.Default()

	// Setup CORS middleware.
	corsConfig := cors.DefaultConfig()

	// Get allowed origins from environment variable.
	// Default to allow all origins if not specified.
	allowedOrigins := os.Getenv("CORS_ALLOWED_ORIGINS")
	if allowedOrigins != "" {
		corsConfig.AllowOrigins = strings.Split(allowedOrigins, ",")
	} else {
		corsConfig.AllowAllOrigins = true
	}

	router.Use(cors.New(corsConfig))

	// Create handler.
	handler := NewHandler(predictionUC)

	// API v1 routes.
	v1 := router.Group("/v1")
	// Tide predictions.
	tides := v1.Group("/tides")
	tides.GET("/predictions", handler.GetPredictions)

	// Constituents.
	v1.GET("/constituents", handler.GetConstituentsList)

	// Health check.
	router.GET("/health", handler.HealthCheck)

	return router
}
