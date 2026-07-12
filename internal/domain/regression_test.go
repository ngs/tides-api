package domain

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Regression test for: nodal corrections (f, u) are evaluated with deltaHours since
// ReferenceTime but interpreted as hours since Unix epoch, shifting the astronomical
// arguments (notably the lunar node N, 18.6-year cycle) by the epoch offset.
//
// Two parameter sets describing the exact same physical tide (same absolute time, phases
// adjusted for the different reference epochs) must produce identical heights, because
// the nodal correction depends only on the absolute time being predicted.
func TestCalculateTideHeight_NodalCorrectionUsesAbsoluteTime(t *testing.T) {
	// Force the built-in nodal coefficients (no external coefficient file).
	t.Setenv("ASTRO_COEFFS_PATH", filepath.Join(t.TempDir(), "missing.json"))

	const speedM2 = 28.9841042 // deg/hr

	refA := time.Unix(0, 0).UTC()                       // Unix epoch
	refB := time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC) // FES epoch

	// Adjust phase so that omega*deltaT + V(ref) - phase is identical for both
	// reference times: phaseB = phaseA + omega*(refA - refB) + V(refB) - V(refA),
	// taken mod 360. The V terms are required because the equilibrium argument
	// is evaluated at the reference epoch (theta = omega*dt + V(t_ref) + u - phi).
	nodal := NewAstronomicalNodalCorrection()
	vA := nodal.GetEquilibriumArgument("M2", refA.Sub(refA).Hours())
	vB := nodal.GetEquilibriumArgument("M2", refB.Sub(refA).Hours())
	phaseA := 0.0
	phaseB := math.Mod(phaseA+speedM2*refA.Sub(refB).Hours()+vB-vA, 360.0)
	if phaseB < 0 {
		phaseB += 360.0
	}

	makeParams := func(ref time.Time, phase float64) PredictionParams {
		return PredictionParams{
			Constituents: []ConstituentParam{
				{
					Name:          "M2",
					AmplitudeM:    1.0,
					PhaseDeg:      phase,
					SpeedDegPerHr: speedM2,
				},
			},
			MSL:             0.0,
			Longitude:       0.0,
			NodalCorrection: NewAstronomicalNodalCorrection(),
			ReferenceTime:   ref,
			PhaseConvention: PhaseConvFESGreenwich,
		}
	}

	paramsA := makeParams(refA, phaseA)
	paramsB := makeParams(refB, phaseB)

	// Sample several absolute times; heights must agree at every one of them.
	base := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 8; i++ {
		at := base.Add(time.Duration(i) * 3 * time.Hour)
		hA := CalculateTideHeight(at, paramsA)
		hB := CalculateTideHeight(at, paramsB)
		if math.Abs(hA-hB) > 1e-6 {
			t.Errorf("nodal correction depends on ReferenceTime: at %v height with epoch ref = %.9f, with FES-2012 ref = %.9f (diff %.9f)",
				at, hA, hB, hA-hB)
		}
	}
}

// Regression test for: FES Greenwich phase convention wrongly adds geographic longitude
// to the phase angle. A Greenwich phase lag already refers phases to the Greenwich
// meridian: h = f*A*cos(V(t) + u - phi), with no lambda term. Changing only Longitude
// must not change the predicted height.
func TestCalculateTideHeight_FESGreenwichIgnoresLongitude(t *testing.T) {
	refTime := time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)

	makeParams := func(lon float64) PredictionParams {
		return PredictionParams{
			Constituents: []ConstituentParam{
				{
					Name:          "M2",
					AmplitudeM:    1.0,
					PhaseDeg:      30.0,
					SpeedDegPerHr: 28.9841042,
				},
			},
			MSL:             0.0,
			Longitude:       lon,
			NodalCorrection: &IdentityNodalCorrection{},
			ReferenceTime:   refTime,
			PhaseConvention: PhaseConvFESGreenwich,
		}
	}

	at := refTime.Add(5 * time.Hour)
	h0 := CalculateTideHeight(at, makeParams(0.0))
	h135 := CalculateTideHeight(at, makeParams(135.5))

	if math.Abs(h0-h135) > 1e-9 {
		t.Errorf("Greenwich phase lag must not depend on longitude: lon=0 gives %.9f, lon=135.5 gives %.9f", h0, h135)
	}
}

// Regression test for: LoadNodalCoeffSet silently accepts non-numeric harmonic keys;
// strconv.Atoi errors are discarded at evaluation time, so a bad key is treated as k=0.
// Loading a coefficient file with a non-numeric key must return an error.
func TestLoadNodalCoeffSet_RejectsNonNumericKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad_coeffs.json")
	content := `{
  "coeffs": [
    {
      "name": "M2",
      "f0": 1.0,
      "u0": 0.0,
      "v0": 0.0,
      "f_cos": {"not-a-number": 0.5}
    }
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	set, err := LoadNodalCoeffSet(path)
	if err == nil {
		t.Errorf("expected error loading nodal coefficients with non-numeric key, got nil (set=%+v)", set)
	}
}
