package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"go.ngs.io/tides-api/internal/adapter/store"
	"go.ngs.io/tides-api/internal/adapter/store/csv"
	"go.ngs.io/tides-api/internal/adapter/store/fes"
	httpHandler "go.ngs.io/tides-api/internal/http"
	"go.ngs.io/tides-api/internal/usecase"
)

const version = "0.1.0"

func main() {
	// Parse command-line flags.
	showHelp := flag.Bool("help", false, "Show usage information")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showHelp {
		printUsage()
		return
	}

	if *showVersion {
		fmt.Printf("tides-api version %s\n", version)
		return
	}

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

// printUsage prints usage information.
func printUsage() {
	fmt.Printf("Tides API Server v%s\n\n", version)
	fmt.Println("USAGE:")
	fmt.Println("  tides-api [flags]")
	fmt.Println()
	fmt.Println("FLAGS:")
	fmt.Println("  -help          Show this help message")
	fmt.Println("  -version       Show version information")
	fmt.Println()
	fmt.Println("ENVIRONMENT VARIABLES:")
	fmt.Println("  PORT           Server port (default: 8080)")
	fmt.Println("  DATA_DIR       CSV data directory (default: ./data)")
	fmt.Println("  FES_DIR        FES NetCDF data directory (default: ./data/fes)")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  # Start server with default settings")
	fmt.Println("  tides-api")
	fmt.Println()
	fmt.Println("  # Start server on custom port")
	fmt.Println("  PORT=3000 tides-api")
	fmt.Println()
	fmt.Println("API ENDPOINTS:")
	fmt.Println("  GET /healthz                   Health check")
	fmt.Println("  GET /v1/constituents           List tidal constituents")
	fmt.Println("  GET /v1/tides/predictions      Get tide predictions")
	fmt.Println()
}
