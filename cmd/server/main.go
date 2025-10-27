// Package main provides the tides API HTTP server.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"go.ngs.io/tides-api/internal/adapter/geoid"
	"go.ngs.io/tides-api/internal/adapter/store"
	"go.ngs.io/tides-api/internal/adapter/store/bathymetry"
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
	gebcoPath := getEnv("BATHYMETRY_GEBCO_PATH", "")
	mssPath := getEnv("BATHYMETRY_MSS_PATH", "")
	geoidPath := getEnv("GEOID_EGM2008_PATH", "")

	log.Printf("Starting Tide API server...")
	log.Printf("Port: %s", port)
	log.Printf("Data directory: %s", dataDir)
	log.Printf("FES directory: %s", fesDir)

	// Initialize stores.
	csvStore := csv.NewConstituentStore(dataDir)
	fesStore := fes.NewStore(fesDir)

	// Cast to interface.
	var csvLoader store.ConstituentLoader = csvStore
	var fesLoader store.ConstituentLoader = fesStore

	// Initialize geoid store (optional, for MSL correction).
	var geoidStore *geoid.Store
	if geoidPath != "" {
		log.Printf("Initializing EGM2008 geoid store")
		log.Printf("  Geoid path: %s", geoidPath)
		geoidStore = geoid.NewStore(geoidPath)
		log.Printf("Geoid store initialized (will apply MSL correction)")
	}

	// Initialize bathymetry store (optional).
	// Paths can be local files or GCS FUSE-mounted paths (e.g., /mnt/bathymetry/gebco.nc).
	var bathyStore bathymetry.Store
	if gebcoPath != "" || mssPath != "" {
		log.Printf("Initializing bathymetry store")
		if gebcoPath != "" {
			log.Printf("  GEBCO path: %s", gebcoPath)
		}
		if mssPath != "" {
			log.Printf("  MSS path: %s", mssPath)
		}
		if geoidStore == nil && mssPath != "" {
			log.Printf("  Warning: MSS data without geoid correction (results will be ellipsoidal)")
		}
		bathyStore = bathymetry.NewLocalStore(gebcoPath, mssPath, geoidStore)
		log.Printf("Bathymetry store initialized")
	} else {
		log.Printf("Bathymetry store disabled (no data paths configured)")
	}

	// Initialize use case.
	predictionUC := usecase.NewPredictionUseCase(csvLoader, fesLoader, bathyStore)

	// Setup router.
	router := httpHandler.SetupRouter(predictionUC)

	// Start server.
	addr := fmt.Sprintf(":%s", port)
	log.Printf("Server listening on %s", addr)
	log.Printf("Health check: http://localhost:%s/health", port)
	log.Printf("API endpoints:")
	log.Printf("  - GET /v1/tides/predictions")
	log.Printf("  - GET /v1/constituents")
	if bathyStore != nil {
		log.Printf("  - GET /v1/bathymetry")
	}

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
	fmt.Println("  PORT                    Server port (default: 8080)")
	fmt.Println("  DATA_DIR                CSV data directory (default: ./data)")
	fmt.Println("  FES_DIR                 FES NetCDF data directory (default: ./data/fes)")
	fmt.Println("  CORS_ALLOWED_ORIGINS    Comma-separated list of allowed origins (default: all origins)")
	fmt.Println("  BATHYMETRY_GEBCO_PATH   Path to GEBCO NetCDF file (optional, can be GCS FUSE mount)")
	fmt.Println("  BATHYMETRY_MSS_PATH     Path to MSS NetCDF file (optional, can be GCS FUSE mount)")
	fmt.Println("  GEOID_EGM2008_PATH      Path to EGM2008 geoid NetCDF file (optional, for MSL correction)")
	fmt.Println()
	fmt.Println("EXAMPLES:")
	fmt.Println("  # Start server with default settings")
	fmt.Println("  tides-api")
	fmt.Println()
	fmt.Println("  # Start server on custom port")
	fmt.Println("  PORT=3000 tides-api")
	fmt.Println()
	fmt.Println("API ENDPOINTS:")
	fmt.Println("  GET /health                    Health check")
	fmt.Println("  GET /v1/constituents           List tidal constituents")
	fmt.Println("  GET /v1/tides/predictions      Get tide predictions")
	fmt.Println("  GET /v1/bathymetry             Get bathymetry and MSL data (if configured)")
	fmt.Println()
}
