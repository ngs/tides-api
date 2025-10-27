package jma

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// JSTLocation is a fixed +09:00 zone used by JMA hourly records.
var JSTLocation = time.FixedZone("JST", 9*60*60)

// HourlyRecord represents a single day of 24 hourly heights in meters.
type HourlyRecord struct {
	Station string
	Time    time.Time // start of day in JST.
	Hourly  [24]float64
	Valid   [24]bool
}

// ParseHourlyLine parses a single fixed-width JMA line into an HourlyRecord.
func ParseHourlyLine(line string) (*HourlyRecord, error) {
	if len(line) < 80 {
		return nil, fmt.Errorf("line too short: %d", len(line))
	}
	var rec HourlyRecord

	for i := 0; i < 24; i++ {
		start := 3 * i
		end := start + 3
		if end > len(line) {
			return nil, fmt.Errorf("unexpected end of line while parsing hour %d", i)
		}
		chunk := strings.TrimSpace(line[start:end])
		chunk = strings.ReplaceAll(chunk, " ", "")
		if chunk == "" || chunk == "999" {
			rec.Hourly[i] = 0
			rec.Valid[i] = false
			continue
		}
		v, err := strconv.Atoi(chunk)
		if err != nil {
			return nil, fmt.Errorf("invalid hourly value '%s' at %d: %w", chunk, i, err)
		}
		rec.Hourly[i] = float64(v) / 100.0
		rec.Valid[i] = true
	}

	yearStr := strings.TrimSpace(line[72:74])
	monthStr := strings.TrimSpace(line[74:76])
	dayStr := strings.TrimSpace(line[76:78])
	station := strings.TrimSpace(line[78:80])

	yearVal, err := strconv.Atoi(yearStr)
	if err != nil {
		return nil, fmt.Errorf("invalid year '%s': %w", yearStr, err)
	}
	monthVal, err := strconv.Atoi(monthStr)
	if err != nil {
		return nil, fmt.Errorf("invalid month '%s': %w", monthStr, err)
	}
	dayVal, err := strconv.Atoi(dayStr)
	if err != nil {
		return nil, fmt.Errorf("invalid day '%s': %w", dayStr, err)
	}

	year := 2000 + yearVal
	if yearVal >= 70 {
		year = 1900 + yearVal
	}

	rec.Station = station
	rec.Time = time.Date(year, time.Month(monthVal), dayVal, 0, 0, 0, 0, JSTLocation)
	return &rec, nil
}

// LoadStationRecords scans reader for lines belonging to the given station code.
func LoadStationRecords(r io.Reader, station string) ([]HourlyRecord, error) {
	station = strings.TrimSpace(station)
	scanner := bufio.NewScanner(r)
	records := make([]HourlyRecord, 0, 366)

	for scanner.Scan() {
		line := scanner.Text()
		rec, err := ParseHourlyLine(line)
		if err != nil {
			continue
		}
		if rec.Station == station {
			records = append(records, *rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to scan JMA data: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no records found for station %s", station)
	}
	return records, nil
}

// LoadStationRecordsFromPath loads data from a local path or HTTP URL.
func LoadStationRecordsFromPath(pathOrURL, station string) ([]HourlyRecord, error) {
	data, err := loadBytes(pathOrURL)
	if err != nil {
		return nil, err
	}
	return LoadStationRecords(bytes.NewReader(data), station)
}

func loadBytes(path string) ([]byte, error) {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
		return io.ReadAll(resp.Body)
	}
	return os.ReadFile(path)
}
