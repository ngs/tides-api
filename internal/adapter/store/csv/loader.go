// Package csv provides CSV-based constituent data loading.
package csv

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.ngs.io/tides-api/internal/domain"
)

// ConstituentStore provides access to tidal constituent data.
type ConstituentStore struct {
	dataDir string
}

// NewConstituentStore creates a new CSV-based constituent store.
func NewConstituentStore(dataDir string) *ConstituentStore {
	return &ConstituentStore{
		dataDir: dataDir,
	}
}

// LoadForStation loads constituent parameters for a named station.
func (s *ConstituentStore) LoadForStation(stationID string) ([]domain.ConstituentParam, error) {
	// Construct file path.
	filename := fmt.Sprintf("%s/mock_%s_constituents.csv", s.dataDir, strings.ToLower(stationID))

	//nolint:gosec // G304: File path constructed from dataDir (config) and stationID (validated).
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open CSV file for station %s: %w", stationID, err)
	}
	defer func() { _ = file.Close() }()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true

	// Read header.
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV header: %w", err)
	}

	// Validate header.
	expectedHeaders := []string{"constituent", "amplitude_m", "phase_deg"}
	if len(header) != len(expectedHeaders) {
		return nil, fmt.Errorf("invalid CSV header: expected %v, got %v", expectedHeaders, header)
	}

	for i, h := range header {
		if h != expectedHeaders[i] {
			return nil, fmt.Errorf("invalid CSV header: expected column %d to be %s, got %s", i, expectedHeaders[i], h)
		}
	}

	// Read data rows.
	constituents := make([]domain.ConstituentParam, 0)

	for {
		record, err := reader.Read()
		if err != nil {
			// EOF is expected.
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("failed to read CSV record: %w", err)
		}

		if len(record) != 3 {
			return nil, fmt.Errorf("invalid CSV record: expected 3 columns, got %d", len(record))
		}

		name := strings.TrimSpace(record[0])
		amplitudeStr := strings.TrimSpace(record[1])
		phaseStr := strings.TrimSpace(record[2])

		// Parse amplitude.
		amplitude, err := strconv.ParseFloat(amplitudeStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid amplitude for constituent %s: %w", name, err)
		}

		// Parse phase.
		phase, err := strconv.ParseFloat(phaseStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid phase for constituent %s: %w", name, err)
		}

		// Get angular speed from standard constituents.
		speed, ok := domain.GetConstituentSpeed(name)
		if !ok {
			return nil, fmt.Errorf("unknown constituent: %s", name)
		}

		constituents = append(constituents, domain.ConstituentParam{
			Name:          name,
			AmplitudeM:    amplitude,
			PhaseDeg:      phase,
			SpeedDegPerHr: speed,
		})
	}

	if len(constituents) == 0 {
		return nil, fmt.Errorf("no constituents found in CSV for station %s", stationID)
	}

	return constituents, nil
}

// LoadForLocation loads constituent parameters for a lat/lon location.
// This is a placeholder for FES integration - currently not supported.
func (s *ConstituentStore) LoadForLocation(_ /* lat */, _ /* lon */ float64) ([]domain.ConstituentParam, error) {
	return nil, fmt.Errorf("CSV store does not support lat/lon queries - use FES store or specify a station_id")
}

// ListStations returns available station IDs.
func (s *ConstituentStore) ListStations() ([]string, error) {
	entries, err := os.ReadDir(s.dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read data directory: %w", err)
	}

	stations := make([]string, 0)
	prefix := "mock_"
	suffix := "_constituents.csv"

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			// Extract station ID.
			stationID := name[len(prefix) : len(name)-len(suffix)]
			stations = append(stations, stationID)
		}
	}

	return stations, nil
}
