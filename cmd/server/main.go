package main

import (
	"fmt"
	"log"
	"os"

	"go.ngs.io/tides-api/internal/adapter/store"
	"go.ngs.io/tides-api/internal/adapter/store/csv"
	"go.ngs.io/tides-api/internal/adapter/store/fes"
	httpHandler "go.ngs.io/tides-api/internal/http"
	"go.ngs.io/tides-api/internal/usecase"
)

func main() {
	// Load configuration from environment.
	port := getEnv("PORT", "8080")
	dataDir := getEnv("DATA_DIR", "./data")
	fesDir := getEnv("FES_DIR", "./data/fes")

	log.Printf("Starting Tide API server...")
	log.Printf("Port: %s", port)
	log.Printf("Data directory: %s", dataDir)
	log.Printf("FES directory: %s", fesDir)

	// Initialize stores.
	csvStore := csv.NewConstituentStore(dataDir)
	fesStore := fes.NewFESStore(fesDir)

	// Cast to interface.
	var csvLoader store.ConstituentLoader = csvStore
	var fesLoader store.ConstituentLoader = fesStore

	// Initialize use case.
	predictionUC := usecase.NewPredictionUseCase(csvLoader, fesLoader)

	// Setup router.
	router := httpHandler.SetupRouter(predictionUC)

	// Start server.
	addr := fmt.Sprintf(":%s", port)
	log.Printf("Server listening on %s", addr)
	log.Printf("Health check: http://localhost:%s/healthz", port)
	log.Printf("API endpoints:")
	log.Printf("  - GET /v1/tides/predictions")
	log.Printf("  - GET /v1/constituents")

	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
