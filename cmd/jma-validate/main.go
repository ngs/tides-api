// Package main validates the fitted station overrides against JMA hourly
// astronomical tide data: it predicts hourly heights with the same basis as
// the API (theta = omega*dt + V(t_ref) + u(t) - phi) and reports the RMSE per
// station. A basis mismatch between fitting and prediction fails at the meter
// level, so this doubles as a regression check whenever the equilibrium
// argument, nodal corrections, or the overrides data change.
//
// JMA TXT files are expected as <txt_dir>/<STATION>.txt, downloadable from
// https://www.data.jma.go.jp/kaiyou/data/db/tide/suisan/txt/<year>/<code>.txt
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"go.ngs.io/tides-api/internal/domain"
	"go.ngs.io/tides-api/internal/jma"
)

type overrideConstituent struct {
	Name       string  `json:"name"`
	AmplitudeM float64 `json:"amplitude_m"`
	PhaseDeg   float64 `json:"phase_deg"`
}

type stationOverride struct {
	Station      string                `json:"station"`
	DatumOffset  float64               `json:"datum_offset_m"`
	Constituents []overrideConstituent `json:"constituents"`
}

type result struct {
	Station string
	RMSE    float64
	N       int
}

func main() {
	overridesPath := flag.String("overrides", "data/jma_station_overrides.json", "Path to station overrides JSON")
	txtDir := flag.String("txt_dir", "tmp/jma_txt", "Directory containing JMA {CODE}.txt files")
	refStr := flag.String("ref", "2012-01-01T00:00:00Z", "Reference epoch used when the overrides were fitted (RFC3339)")
	worst := flag.Int("worst", 10, "Number of worst stations to list")
	maxMeanRMSE := flag.Float64("max_mean_rmse", 0, "Exit non-zero if the mean RMSE (m) exceeds this value (0 = disabled)")
	flag.Parse()

	ref, err := time.Parse(time.RFC3339, *refStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid -ref: %v\n", err)
		os.Exit(2)
	}

	raw, err := os.ReadFile(*overridesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read overrides: %v\n", err)
		os.Exit(1)
	}
	var overrides []stationOverride
	if err := json.Unmarshal(raw, &overrides); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse overrides: %v\n", err)
		os.Exit(1)
	}

	results := make([]result, 0, len(overrides))
	for _, ov := range overrides {
		r, err := validateStation(ov, *txtDir, ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", ov.Station, err)
			continue
		}
		results = append(results, r)
	}
	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no stations validated")
		os.Exit(1)
	}

	sort.Slice(results, func(i, j int) bool { return results[i].RMSE < results[j].RMSE })
	var sum float64
	for _, r := range results {
		sum += r.RMSE
	}
	mean := sum / float64(len(results))

	fmt.Printf("stations=%d mean_rmse=%.4fm median_rmse=%.4fm best=%s(%.4fm) worst=%s(%.4fm)\n",
		len(results), mean, results[len(results)/2].RMSE,
		results[0].Station, results[0].RMSE,
		results[len(results)-1].Station, results[len(results)-1].RMSE)
	if *worst > 0 {
		fmt.Printf("worst %d:\n", *worst)
		start := len(results) - *worst
		if start < 0 {
			start = 0
		}
		for _, r := range results[start:] {
			fmt.Printf("  %s rmse=%.4fm n=%d\n", r.Station, r.RMSE, r.N)
		}
	}

	if *maxMeanRMSE > 0 && mean > *maxMeanRMSE {
		fmt.Fprintf(os.Stderr, "mean RMSE %.4fm exceeds threshold %.4fm\n", mean, *maxMeanRMSE)
		os.Exit(1)
	}
}

func validateStation(ov stationOverride, txtDir string, ref time.Time) (result, error) {
	records, err := jma.LoadStationRecordsFromPath(txtDir+"/"+ov.Station+".txt", ov.Station)
	if err != nil {
		return result{}, err
	}

	constituents := make([]domain.ConstituentParam, 0, len(ov.Constituents))
	for _, c := range ov.Constituents {
		speed, ok := domain.GetConstituentSpeed(c.Name)
		if !ok {
			continue
		}
		constituents = append(constituents, domain.ConstituentParam{
			Name:          c.Name,
			AmplitudeM:    c.AmplitudeM,
			PhaseDeg:      c.PhaseDeg,
			SpeedDegPerHr: speed,
		})
	}
	params := domain.PredictionParams{
		Constituents:    constituents,
		MSL:             ov.DatumOffset,
		NodalCorrection: domain.NewAstronomicalNodalCorrection(),
		ReferenceTime:   ref,
	}

	var sumSq float64
	var n int
	for _, rec := range records {
		for hour := 0; hour < 24; hour++ {
			if !rec.Valid[hour] {
				continue
			}
			t := rec.Time.Add(time.Duration(hour) * time.Hour).UTC()
			diff := domain.CalculateTideHeight(t, params) - rec.Hourly[hour]
			sumSq += diff * diff
			n++
		}
	}
	if n == 0 {
		return result{}, fmt.Errorf("no valid samples")
	}
	return result{Station: ov.Station, RMSE: math.Sqrt(sumSq / float64(n)), N: n}, nil
}
