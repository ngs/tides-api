package domain

import (
	"math"
	"sort"
	"time"
)

// TideLevel represents a single tide height prediction at a specific time.
type TideLevel struct {
	Time    time.Time
	HeightM float64
}

// LocationMetadata holds additional metadata about a location.
type LocationMetadata struct {
	MSL        float64  // Mean Sea Level in meters (relative to reference datum).
	DepthM     *float64 // Seabed depth in meters (optional, positive value indicates depth below MSL).
	DatumName  string   // Name of the reference datum (e.g., "EGM2008", "WGS84").
	SourceName string   // Data source name (e.g., "GEBCO 2024", "DTU21 MSS").
}

// Extrema represents high and low tide events.
type Extrema struct {
	Highs []TideLevel
	Lows  []TideLevel
}

// PredictionParams holds all parameters needed for tide prediction.
type PredictionParams struct {
    Constituents    []ConstituentParam
    MSL             float64         // Mean Sea Level offset in meters.
    Longitude       float64         // Longitude in degrees (for Greenwich phase correction).
    NodalCorrection NodalCorrection // Interface for nodal corrections.
    ReferenceTime   time.Time       // Reference time for phase (usually Unix epoch or local epoch).
    PhaseConvention PhaseConvention // Phase handling convention.
}

// PhaseConvention selects the phase formula to use.
// - PhaseConvFESGreenwich: use Greenwich phase lag with longitude correction (typical for FES)
//   h(t) = f A cos(ωΔt - φ + λ + u) + MSL
// - PhaseConvVu: use equilibrium argument V + nodal correction u
//   h(t) = f A cos(ωΔt + (V + u) - φ) + MSL
type PhaseConvention int

const (
	// PhaseConvFESGreenwich uses Greenwich phase lag with longitude correction.
    PhaseConvFESGreenwich PhaseConvention = iota
	// PhaseConvVu uses equilibrium argument V + nodal correction u.
    PhaseConvVu
)

// CalculateTideHeight computes the tide height at a specific time using harmonic analysis
// η(t) = Σ f_k * A_k * cos(ω_k * Δt + φ_k - u_k) + MSL
// where:
//   - f_k, u_k are nodal corrections (amplitude factor and phase correction)
//   - A_k is amplitude in meters
//   - ω_k is angular speed in degrees per hour
//   - φ_k is phase in degrees
//   - Δt is hours since reference time
func CalculateTideHeight(t time.Time, params PredictionParams) float64 {
    if params.NodalCorrection == nil {
        params.NodalCorrection = &IdentityNodalCorrection{}
    }

    deltaHours := t.Sub(params.ReferenceTime).Hours()
    height := params.MSL

    for _, c := range params.Constituents {
        // Get nodal corrections.
        f, u := params.NodalCorrection.GetFactors(c.Name, deltaHours)

        // Calculate phase angle in degrees based on convention.
        var phaseAngleDeg float64
        switch params.PhaseConvention {
        case PhaseConvFESGreenwich:
            // FES Greenwich phase lag φ with geographic longitude correction.
            // h(t) = f A cos(ωΔt - φ + λ + u)
            phaseAngleDeg = c.SpeedDegPerHr*deltaHours - c.PhaseDeg + params.Longitude + u
        default:
            // Use equilibrium argument V + u (if provided by nodal correction). Avoid longitude.
            v := params.NodalCorrection.GetEquilibriumArgument(c.Name, deltaHours)
            phaseAngleDeg = c.SpeedDegPerHr*deltaHours + v + u - c.PhaseDeg
        }

        // Convert to radians and calculate contribution.
        phaseAngleRad := Deg2Rad(phaseAngleDeg)
        contribution := f * c.AmplitudeM * math.Cos(phaseAngleRad)

        height += contribution
    }

    return height
}

// GeneratePredictions creates a time series of tide predictions.
func GeneratePredictions(start, end time.Time, interval time.Duration, params PredictionParams) []TideLevel {
	predictions := make([]TideLevel, 0)

	for t := start; !t.After(end); t = t.Add(interval) {
		height := CalculateTideHeight(t, params)
		predictions = append(predictions, TideLevel{
			Time:    t,
			HeightM: height,
		})
	}

	return predictions
}

// FindExtrema identifies high and low tides from a time series.
// Uses first derivative sign change to detect peaks and troughs.
func FindExtrema(predictions []TideLevel) Extrema {
	if len(predictions) < 3 {
		return Extrema{
			Highs: []TideLevel{},
			Lows:  []TideLevel{},
		}
	}

	highs := make([]TideLevel, 0)
	lows := make([]TideLevel, 0)

	// Use first derivative (finite difference) to find sign changes.
	for i := 1; i < len(predictions)-1; i++ {
		prev := predictions[i-1].HeightM
		curr := predictions[i].HeightM
		next := predictions[i+1].HeightM

		// Check for local maximum (peak).
		if curr > prev && curr > next {
			highs = append(highs, predictions[i])
		}

		// Check for local minimum (trough).
		if curr < prev && curr < next {
			lows = append(lows, predictions[i])
		}

		// Handle plateau cases (curr == prev or curr == next).
		// For simplicity, we skip these in MVP.
	}

	return Extrema{
		Highs: highs,
		Lows:  lows,
	}
}

// RefineExtremum performs parabolic interpolation to get a more accurate extremum.
// Uses three points around the discrete extremum to fit a parabola.
// Returns the interpolated time and height.
func RefineExtremum(before, peak, after TideLevel) (time.Time, float64) {
	// Time spacing in hours.
	dt1 := peak.Time.Sub(before.Time).Hours()
	dt2 := after.Time.Sub(peak.Time).Hours()

	// For simplicity, assume uniform spacing.
	if math.Abs(dt1-dt2) > 1e-6 {
		// Non-uniform spacing - return discrete peak.
		return peak.Time, peak.HeightM
	}

	// Parabolic interpolation
	// y = a*x^2 + b*x + c
	// Vertex at x = -b/(2a).
	h0, h1, h2 := before.HeightM, peak.HeightM, after.HeightM

	// Using finite differences.
	a := (h2 - 2*h1 + h0) / (2 * dt1 * dt1)
	b := (h2 - h0) / (2 * dt1)

	if math.Abs(a) < 1e-10 {
		// Nearly linear - return discrete peak.
		return peak.Time, peak.HeightM
	}

	// Time offset from peak for the vertex.
	dtVertex := -b / (2 * a)

	// Clamp to reasonable range (within interval).
	if math.Abs(dtVertex) > dt1 {
		return peak.Time, peak.HeightM
	}

	refinedTime := peak.Time.Add(time.Duration(dtVertex * float64(time.Hour)))
	refinedHeight := h1 + b*dtVertex + a*dtVertex*dtVertex

	return refinedTime, refinedHeight
}

// RefineExtrema applies parabolic interpolation to all extrema.
func RefineExtrema(predictions []TideLevel, extrema Extrema) Extrema {
	if len(predictions) < 3 {
		return extrema
	}

	// Create a map for quick lookup.
	predMap := make(map[time.Time]int)
	for i, p := range predictions {
		predMap[p.Time] = i
	}

	refinedHighs := make([]TideLevel, 0, len(extrema.Highs))
	for _, high := range extrema.Highs {
		idx, ok := predMap[high.Time]
		if !ok || idx < 1 || idx >= len(predictions)-1 {
			refinedHighs = append(refinedHighs, high)
			continue
		}

		refinedTime, refinedHeight := RefineExtremum(
			predictions[idx-1],
			predictions[idx],
			predictions[idx+1],
		)

		refinedHighs = append(refinedHighs, TideLevel{
			Time:    refinedTime,
			HeightM: refinedHeight,
		})
	}

	refinedLows := make([]TideLevel, 0, len(extrema.Lows))
	for _, low := range extrema.Lows {
		idx, ok := predMap[low.Time]
		if !ok || idx < 1 || idx >= len(predictions)-1 {
			refinedLows = append(refinedLows, low)
			continue
		}

		refinedTime, refinedHeight := RefineExtremum(
			predictions[idx-1],
			predictions[idx],
			predictions[idx+1],
		)

		refinedLows = append(refinedLows, TideLevel{
			Time:    refinedTime,
			HeightM: refinedHeight,
		})
	}

	// Sort by time.
	sort.Slice(refinedHighs, func(i, j int) bool {
		return refinedHighs[i].Time.Before(refinedHighs[j].Time)
	})
	sort.Slice(refinedLows, func(i, j int) bool {
		return refinedLows[i].Time.Before(refinedLows[j].Time)
	})

	return Extrema{
		Highs: refinedHighs,
		Lows:  refinedLows,
	}
}
