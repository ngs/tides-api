package usecase

import (
	"encoding/json"
	"math"
	"os"
	"sync"

	"go.ngs.io/tides-api/internal/domain"
)

// --- Datum offsets (nearest neighbor) ---

type datumOffsetEntry struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	OffsetM float64 `json:"offset_m"`
}

var (
	datumOnce  sync.Once
	datumTable []datumOffsetEntry
)

func getAutoDatumOffset(lat, lon float64) (float64, bool) {
	datumOnce.Do(func() {
		path := os.Getenv("DATUM_OFFSETS_PATH")
		if path == "" {
			path = "data/jma_datum_offsets.json"
		}
		if b, err := os.ReadFile(path); err == nil {
			var entries []datumOffsetEntry
			if err := json.Unmarshal(b, &entries); err == nil {
				datumTable = entries
			}
		}
	})
	if len(datumTable) == 0 {
		return 0, false
	}
	bestDist := math.MaxFloat64
	bestOffset := 0.0
	for _, entry := range datumTable {
		d := haversineKm(lat, lon, entry.Lat, entry.Lon)
		if d < bestDist {
			bestDist = d
			bestOffset = entry.OffsetM
		}
	}
	if bestDist <= 80 {
		return bestOffset, true
	}
	return 0, false
}

// --- Station constituent overrides ---

type overrideConstituent struct {
	Name       string  `json:"name"`
	AmplitudeM float64 `json:"amplitude_m"`
	PhaseDeg   float64 `json:"phase_deg"`
}

type stationOverrideEntry struct {
	Name         string                `json:"name"`
	Station      string                `json:"station,omitempty"`
	Lat          float64               `json:"lat"`
	Lon          float64               `json:"lon"`
	RadiusKm     float64               `json:"radius_km"`
	DatumOffset  *float64              `json:"datum_offset_m,omitempty"`
	Constituents []overrideConstituent `json:"constituents"`
}

var (
	overridesOnce  sync.Once
	overridesTable []stationOverrideEntry
)

func loadOverrides() {
	path := os.Getenv("STATION_OVERRIDES_PATH")
	if path == "" {
		path = "data/jma_station_overrides.json"
	}
	if b, err := os.ReadFile(path); err == nil {
		var entries []stationOverrideEntry
		if err := json.Unmarshal(b, &entries); err == nil {
			overridesTable = entries
		}
	}
}

func getStationOverride(lat, lon float64) (*stationOverrideEntry, bool) {
	overridesOnce.Do(loadOverrides)
	if len(overridesTable) == 0 {
		return nil, false
	}
	bestDist := math.MaxFloat64
	var best *stationOverrideEntry
	for i := range overridesTable {
		entry := &overridesTable[i]
		radius := entry.RadiusKm
		if radius == 0 {
			radius = 40
		}
		d := haversineKm(lat, lon, entry.Lat, entry.Lon)
		if d <= radius && d < bestDist {
			bestDist = d
			best = entry
		}
	}
	if best == nil {
		return nil, false
	}
	return best, true
}

func applyStationOverride(lat, lon float64, constituents []domain.ConstituentParam, msl *float64) []domain.ConstituentParam {
	override, ok := getStationOverride(lat, lon)
	if !ok {
		return constituents
	}

	adjusted := make([]domain.ConstituentParam, len(constituents))
	copy(adjusted, constituents)

	if override.DatumOffset != nil && msl != nil {
		*msl += *override.DatumOffset
	}

	index := make(map[string]int, len(adjusted))
	for i, c := range adjusted {
		index[c.Name] = i
	}

	for _, ov := range override.Constituents {
		if idx, ok := index[ov.Name]; ok {
			adjusted[idx].AmplitudeM = ov.AmplitudeM
			adjusted[idx].PhaseDeg = wrapPhase(ov.PhaseDeg)
			continue
		}
		speed, ok := domain.GetConstituentSpeed(ov.Name)
		if !ok {
			continue
		}
		adjusted = append(adjusted, domain.ConstituentParam{
			Name:          ov.Name,
			AmplitudeM:    ov.AmplitudeM,
			PhaseDeg:      wrapPhase(ov.PhaseDeg),
			SpeedDegPerHr: speed,
		})
	}

	return adjusted
}

func wrapPhase(deg float64) float64 {
	for deg < 0 {
		deg += 360
	}
	for deg >= 360 {
		deg -= 360
	}
	return deg
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	toRad := func(x float64) float64 { return x * math.Pi / 180.0 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}
