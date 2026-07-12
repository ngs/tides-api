package jma

import (
	"strings"
	"testing"
	"time"
)

// buildLine constructs a valid 80-character fixed-width JMA hourly line from
// 24 three-character height chunks plus yy/mm/dd/station metadata.
func buildLine(t *testing.T, chunks [24]string, yy, mm, dd, station string) string {
	t.Helper()
	var b strings.Builder
	for i, c := range chunks {
		if len(c) != 3 {
			t.Fatalf("chunk %d must be 3 chars, got %q", i, c)
		}
		b.WriteString(c)
	}
	b.WriteString(yy)
	b.WriteString(mm)
	b.WriteString(dd)
	b.WriteString(station)
	line := b.String()
	if len(line) != 80 {
		t.Fatalf("built line must be 80 chars, got %d", len(line))
	}
	return line
}

// Regression test for: JMA missing-value handling in ParseHourlyLine
// (jma.go:45). This is a specification-pinning test (expected to PASS):
// the sentinel "999" and blank chunks must be treated as missing data with
// Valid=false, never as a real height, so that downstream consumers can
// exclude them from statistics.
func TestParseHourlyLine_999AndBlankAreInvalid(t *testing.T) {
	var chunks [24]string
	for i := range chunks {
		chunks[i] = "150" // 1.50 m
	}
	chunks[3] = "999" // JMA missing-value sentinel.
	chunks[7] = "   " // Blank chunk is also missing.

	line := buildLine(t, chunks, "25", "10", "27", "KZ")

	rec, err := ParseHourlyLine(line)
	if err != nil {
		t.Fatalf("ParseHourlyLine: %v", err)
	}

	if rec.Station != "KZ" {
		t.Errorf("Station = %q, want %q", rec.Station, "KZ")
	}
	wantTime := time.Date(2025, 10, 27, 0, 0, 0, 0, JSTLocation)
	if !rec.Time.Equal(wantTime) {
		t.Errorf("Time = %v, want %v", rec.Time, wantTime)
	}

	for i := 0; i < 24; i++ {
		switch i {
		case 3, 7:
			if rec.Valid[i] {
				t.Errorf("hour %d: Valid = true, want false (missing data must not be treated as a real height)", i)
			}
		default:
			if !rec.Valid[i] {
				t.Errorf("hour %d: Valid = false, want true", i)
			}
			if rec.Hourly[i] != 1.5 {
				t.Errorf("hour %d: Hourly = %v, want 1.5", i, rec.Hourly[i])
			}
		}
	}

	// The sentinel value 999 must never surface as 9.99m.
	if rec.Hourly[3] == 9.99 {
		t.Errorf("hour 3: sentinel 999 leaked through as height %v", rec.Hourly[3])
	}
}
