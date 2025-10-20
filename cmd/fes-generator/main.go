package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fhs/go-netcdf/netcdf"
)

// ConstituentData holds amplitude and phase for a constituent
type ConstituentData struct {
	Name      string
	Amplitude float64 // meters
	Phase     float64 // degrees
}

// RegionalGrid defines the geographic bounds and resolution
type RegionalGrid struct {
	LatMin     float64
	LatMax     float64
	LonMin     float64
	LonMax     float64
	Resolution float64 // degrees
}

func main() {
	// Command line flags
	csvPath := flag.String("csv", "./data/mock_tokyo_constituents.csv", "Path to CSV file with constituent data")
	outDir := flag.String("out", "./data/fes", "Output directory for NetCDF files")
	region := flag.String("region", "japan", "Region: japan, global, or custom")
	latMin := flag.Float64("lat-min", 20.0, "Minimum latitude (custom region)")
	latMax := flag.Float64("lat-max", 50.0, "Maximum latitude (custom region)")
	lonMin := flag.Float64("lon-min", 120.0, "Minimum longitude (custom region)")
	lonMax := flag.Float64("lon-max", 150.0, "Maximum longitude (custom region)")
	resolution := flag.Float64("resolution", 0.1, "Grid resolution in degrees")
	tokyoLat := flag.Float64("tokyo-lat", 35.6762, "Tokyo latitude (reference point)")
	tokyoLon := flag.Float64("tokyo-lon", 139.6503, "Tokyo longitude (reference point)")

	flag.Parse()

	// Define grid based on region
	var grid RegionalGrid
	switch *region {
	case "japan":
		grid = RegionalGrid{
			LatMin:     20.0,
			LatMax:     50.0,
			LonMin:     120.0,
			LonMax:     150.0,
			Resolution: *resolution,
		}
	case "global":
		grid = RegionalGrid{
			LatMin:     -90.0,
			LatMax:     90.0,
			LonMin:     -180.0,
			LonMax:     180.0,
			Resolution: 0.5, // Lower resolution for global
		}
	case "custom":
		grid = RegionalGrid{
			LatMin:     *latMin,
			LatMax:     *latMax,
			LonMin:     *lonMin,
			LonMax:     *lonMax,
			Resolution: *resolution,
		}
	default:
		log.Fatalf("Unknown region: %s (use japan, global, or custom)", *region)
	}

	// Read constituent data from CSV
	constituents, err := readConstituentCSV(*csvPath)
	if err != nil {
		log.Fatalf("Failed to read CSV: %v", err)
	}

	log.Printf("Loaded %d constituents from %s", len(constituents), *csvPath)
	log.Printf("Generating FES NetCDF files for region: %s", *region)
	log.Printf("Grid: %.1f°-%.1f°N, %.1f°-%.1f°E, resolution: %.2f°",
		grid.LatMin, grid.LatMax, grid.LonMin, grid.LonMax, grid.Resolution)

	// Create output directory
	if err := os.MkdirAll(*outDir, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// Generate NetCDF files for each constituent
	for _, constituent := range constituents {
		if err := generateNetCDF(constituent, grid, *tokyoLat, *tokyoLon, *outDir); err != nil {
			log.Printf("Warning: Failed to generate NetCDF for %s: %v", constituent.Name, err)
			continue
		}
		log.Printf("✓ Generated %s_amplitude.nc and %s_phase.nc",
			strings.ToLower(constituent.Name), strings.ToLower(constituent.Name))
	}

	// Print summary
	log.Printf("\n=== Generation Complete ===")
	log.Printf("Files created in: %s", *outDir)
	log.Printf("Grid size: %d × %d points",
		int((grid.LatMax-grid.LatMin)/grid.Resolution)+1,
		int((grid.LonMax-grid.LonMin)/grid.Resolution)+1)

	// Estimate file sizes
	nLat := int((grid.LatMax-grid.LatMin)/grid.Resolution) + 1
	nLon := int((grid.LonMax-grid.LonMin)/grid.Resolution) + 1
	bytesPerFile := nLat * nLon * 8 // 8 bytes per float64
	totalMB := float64(bytesPerFile*len(constituents)*2) / 1024 / 1024
	log.Printf("Total size: ~%.1f MB (%d constituents × 2 files)", totalMB, len(constituents))
}

// readConstituentCSV reads constituent data from CSV file
func readConstituentCSV(path string) ([]ConstituentData, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true

	// Read header
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}

	// Validate header
	if len(header) != 3 || header[0] != "constituent" ||
		header[1] != "amplitude_m" || header[2] != "phase_deg" {
		return nil, fmt.Errorf("invalid CSV header: %v", header)
	}

	// Read data
	var constituents []ConstituentData
	for {
		record, err := reader.Read()
		if err != nil {
			break // EOF
		}

		if len(record) != 3 {
			continue
		}

		amplitude, err := strconv.ParseFloat(strings.TrimSpace(record[1]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid amplitude for %s: %v", record[0], err)
		}

		phase, err := strconv.ParseFloat(strings.TrimSpace(record[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid phase for %s: %v", record[0], err)
		}

		constituents = append(constituents, ConstituentData{
			Name:      strings.TrimSpace(record[0]),
			Amplitude: amplitude,
			Phase:     phase,
		})
	}

	return constituents, nil
}

// generateNetCDF creates amplitude and phase NetCDF files for a constituent
func generateNetCDF(constituent ConstituentData, grid RegionalGrid, tokyoLat, tokyoLon float64, outDir string) error {
	// Create lat/lon arrays
	nLat := int((grid.LatMax-grid.LatMin)/grid.Resolution) + 1
	nLon := int((grid.LonMax-grid.LonMin)/grid.Resolution) + 1

	lat := make([]float64, nLat)
	for i := 0; i < nLat; i++ {
		lat[i] = grid.LatMin + float64(i)*grid.Resolution
	}

	lon := make([]float64, nLon)
	for i := 0; i < nLon; i++ {
		lon[i] = grid.LonMin + float64(i)*grid.Resolution
	}

	// Generate amplitude and phase grids with spatial variation
	amplitude := make([]float64, nLat*nLon)
	phase := make([]float64, nLat*nLon)

	for i := 0; i < nLat; i++ {
		for j := 0; j < nLon; j++ {
			idx := i*nLon + j

			// Distance from Tokyo (reference point)
			latDist := lat[i] - tokyoLat
			lonDist := lon[j] - tokyoLon
			dist := math.Sqrt(latDist*latDist + lonDist*lonDist)

			// Amplitude: decrease with distance from Tokyo, add smooth variation
			// Use cosine taper: 100% at Tokyo, ~70% at 10° distance
			distFactor := math.Cos(dist * math.Pi / 20.0)
			if distFactor < 0.5 {
				distFactor = 0.5 // Minimum 50% of Tokyo value
			}

			// Add smooth spatial variation (sinusoidal patterns)
			spatialVar := 1.0 +
				0.15*math.Sin(lat[i]*math.Pi/15.0) +
				0.1*math.Cos(lon[j]*math.Pi/20.0) +
				0.05*math.Sin((lat[i]+lon[j])*math.Pi/25.0)

			amplitude[idx] = constituent.Amplitude * distFactor * spatialVar

			// Phase: gradual shift with distance and geographic variation
			phaseShift := dist * 2.0 // 2 degrees per degree distance
			spatialPhase :=
				10.0*math.Sin(lat[i]*math.Pi/30.0) +
				8.0*math.Cos(lon[j]*math.Pi/40.0)

			phase[idx] = math.Mod(constituent.Phase+phaseShift+spatialPhase, 360.0)
			if phase[idx] < 0 {
				phase[idx] += 360.0
			}
		}
	}

	// Write amplitude file
	ampPath := filepath.Join(outDir, fmt.Sprintf("%s_amplitude.nc", strings.ToLower(constituent.Name)))
	if err := writeNetCDF(ampPath, lat, lon, amplitude, nLat, nLon, "amplitude", "meters", constituent.Name); err != nil {
		return err
	}

	// Write phase file
	phaPath := filepath.Join(outDir, fmt.Sprintf("%s_phase.nc", strings.ToLower(constituent.Name)))
	if err := writeNetCDF(phaPath, lat, lon, phase, nLat, nLon, "phase", "degrees", constituent.Name); err != nil {
		return err
	}

	return nil
}

// writeNetCDF writes a NetCDF file with the given data
func writeNetCDF(path string, lat, lon, data []float64, nLat, nLon int, varName, units, constituent string) error {
	// Create NetCDF file
	ds, err := netcdf.CreateFile(path, netcdf.CLOBBER|netcdf.NETCDF4)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer ds.Close()

	// Create dimensions
	latDim, err := ds.AddDim("lat", uint64(nLat))
	if err != nil {
		return err
	}

	lonDim, err := ds.AddDim("lon", uint64(nLon))
	if err != nil {
		return err
	}

	// Create coordinate variables
	latVar, err := ds.AddVar("lat", netcdf.DOUBLE, []netcdf.Dim{latDim})
	if err != nil {
		return err
	}
	latVar.WriteFloat64s(lat)
	// Attributes will be optional for MVP

	lonVar, err := ds.AddVar("lon", netcdf.DOUBLE, []netcdf.Dim{lonDim})
	if err != nil {
		return err
	}
	lonVar.WriteFloat64s(lon)

	// Create data variable
	dataVar, err := ds.AddVar(varName, netcdf.DOUBLE, []netcdf.Dim{latDim, lonDim})
	if err != nil {
		return err
	}
	dataVar.WriteFloat64s(data)

	// Note: Attributes are optional for basic functionality
	// The FES loader will work with just lat, lon, and data variables

	return nil
}
