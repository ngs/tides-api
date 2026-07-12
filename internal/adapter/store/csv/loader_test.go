package csv

import (
	"os"
	"path/filepath"
	"testing"
)

const validConstituentCSV = "constituent,amplitude_m,phase_deg\nM2,1.0,0.0\n"

// Regression test for: stationID is interpolated into the CSV file path without
// validation, allowing path traversal ("..", "/") to open files outside dataDir.
// A stationID containing path separators or ".." must be rejected with an error.
func TestLoadForStation_RejectsPathTraversal(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	//nolint:gosec // G301: Standard test directory permissions.
	if err := os.MkdirAll(filepath.Join(dataDir, "mock_e"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Valid CSV placed OUTSIDE dataDir - must never be readable via stationID.
	evilPath := filepath.Join(base, "mock_evil_constituents.csv")
	//nolint:gosec // G306: Test file with standard permissions.
	if err := os.WriteFile(evilPath, []byte(validConstituentCSV), 0o644); err != nil {
		t.Fatalf("write evil csv: %v", err)
	}

	s := NewConstituentStore(dataDir)

	// Resolves to <dataDir>/mock_e/../../mock_evil_constituents.csv == <base>/mock_evil_constituents.csv.
	params, err := s.LoadForStation("e/../../mock_evil")
	if err == nil {
		t.Fatalf("expected error for stationID containing path traversal, got params: %+v", params)
	}
}

// Regression test for: stationID containing a path separator escapes the flat
// mock_<station>_constituents.csv naming scheme and opens files in subdirectories.
func TestLoadForStation_RejectsSlashInStationID(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	subDir := filepath.Join(dataDir, "mock_sub")
	//nolint:gosec // G301: Standard test directory permissions.
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	//nolint:gosec // G306: Test file with standard permissions.
	if err := os.WriteFile(filepath.Join(subDir, "evil_constituents.csv"), []byte(validConstituentCSV), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	s := NewConstituentStore(dataDir)

	// Resolves to <dataDir>/mock_sub/evil_constituents.csv.
	params, err := s.LoadForStation("sub/evil")
	if err == nil {
		t.Fatalf("expected error for stationID containing '/', got params: %+v", params)
	}
}
