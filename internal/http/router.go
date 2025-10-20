package http

import (
	"github.com/gin-gonic/gin"
	"tide-api/internal/usecase"
)

// SetupRouter creates and configures the Gin router
func SetupRouter(predictionUC *usecase.PredictionUseCase) *gin.Engine {
	// Set Gin to release mode for production
	// gin.SetMode(gin.ReleaseMode)

	router := gin.Default()

	// Create handler
	handler := NewHandler(predictionUC)

	// API v1 routes
	v1 := router.Group("/v1")
	{
		// Tide predictions
		tides := v1.Group("/tides")
		{
			tides.GET("/predictions", handler.GetPredictions)
		}

		// Constituents
		v1.GET("/constituents", handler.GetConstituentsList)
	}

	// Health check
	router.GET("/healthz", handler.HealthCheck)

	return router
}
