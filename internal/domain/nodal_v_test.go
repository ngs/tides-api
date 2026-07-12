package domain

import (
	"math"
	"testing"
	"time"
)

// absHours converts a time to hours since the Unix epoch.
func absHours(t time.Time) float64 {
	return t.Sub(time.Unix(0, 0).UTC()).Hours()
}

// angularDiff returns the smallest absolute angular difference between two
// angles in degrees (result in [0, 180]).
func angularDiff(a, b float64) float64 {
	d := math.Mod(a-b, 360.0)
	if d < 0 {
		d += 360.0
	}
	if d > 180.0 {
		d = 360.0 - d
	}
	return d
}

// TestEquilibriumArgument_S2ZeroAtUTMidnightAndNoon verifies that V(S2) = 2T
// vanishes (mod 360) at 00:00 and 12:00 UT on arbitrary dates. This pins the
// hour-angle convention: T = 180 deg at 00:00 UT, so 2T = 360 = 0 (mod 360).
func TestEquilibriumArgument_S2ZeroAtUTMidnightAndNoon(t *testing.T) {
	nodal := NewAstronomicalNodalCorrection()

	dates := []time.Time{
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2031, 11, 30, 0, 0, 0, 0, time.UTC),
	}

	for _, d := range dates {
		for _, hh := range []int{0, 12} {
			at := d.Add(time.Duration(hh) * time.Hour)
			v := nodal.GetEquilibriumArgument("S2", absHours(at))
			if diff := angularDiff(v, 0); diff > 1e-6 {
				t.Errorf("V(S2) at %v UT: expected 0 (mod 360), got %.9f (diff %.9f)", at, v, diff)
			}
		}
	}
}

// TestEquilibriumArgument_NormalizedRange verifies V is normalized to [0, 360)
// for all standard constituents, and that unknown constituents return 0.
func TestEquilibriumArgument_NormalizedRange(t *testing.T) {
	nodal := NewAstronomicalNodalCorrection()
	at := absHours(time.Date(2026, 7, 4, 9, 30, 0, 0, time.UTC))

	for name := range StandardConstituents {
		v := nodal.GetEquilibriumArgument(name, at)
		if v < 0 || v >= 360.0 {
			t.Errorf("V(%s) = %.9f out of [0, 360)", name, v)
		}
	}

	if v := nodal.GetEquilibriumArgument("NOPE99", at); v != 0 {
		t.Errorf("V for unknown constituent: expected 0, got %.9f", v)
	}
}

// TestEquilibriumArgument_CompoundRelations verifies the shallow-water
// constituents' equilibrium arguments compose from their parents:
// M4 = 2*M2, M6 = 3*M2, S4 = 2*S2, MN4 = M2+N2, MS4 = M2+S2, MK3 = M2+K1.
func TestEquilibriumArgument_CompoundRelations(t *testing.T) {
	nodal := NewAstronomicalNodalCorrection()

	times := []time.Time{
		time.Date(1999, 12, 31, 23, 0, 0, 0, time.UTC),
		time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 6, 45, 0, 0, time.UTC),
	}

	relations := []struct {
		name    string
		parents []string
		mult    []float64
	}{
		{"M4", []string{"M2"}, []float64{2}},
		{"M6", []string{"M2"}, []float64{3}},
		{"S4", []string{"S2"}, []float64{2}},
		{constMN4, []string{"M2", "N2"}, []float64{1, 1}},
		{constMS4, []string{"M2", "S2"}, []float64{1, 1}},
		{constMK3, []string{"M2", "K1"}, []float64{1, 1}},
	}

	for _, at := range times {
		th := absHours(at)
		for _, rel := range relations {
			want := 0.0
			for i, p := range rel.parents {
				want += rel.mult[i] * nodal.GetEquilibriumArgument(p, th)
			}
			got := nodal.GetEquilibriumArgument(rel.name, th)
			if diff := angularDiff(got, want); diff > 1e-6 {
				t.Errorf("V(%s) at %v: expected %.9f (mod 360) from parents, got %.9f (diff %.9f)",
					rel.name, at, math.Mod(want, 360), got, diff)
			}
		}
	}
}

// TestEquilibriumArgument_DiurnalSemidiurnalIdentities verifies the sign
// convention of the +-90 deg offsets (Schureman Table 2). In that convention
// the diurnal pairs recombine into the semidiurnal parents with the offsets
// cancelling exactly:
//
//	V(K1) + V(O1) = V(M2), V(K1) + V(P1) = V(S2), V(K1) + V(Q1) = V(N2)
//
// and the long-period constituents satisfy V(Ssa) = 2*V(Sa) and
// V(K2) = V(S2) + 2*V(Sa).
func TestEquilibriumArgument_DiurnalSemidiurnalIdentities(t *testing.T) {
	nodal := NewAstronomicalNodalCorrection()

	times := []time.Time{
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 15, 17, 20, 0, 0, time.UTC),
	}

	identities := []struct {
		desc  string
		left  []string
		right []string
	}{
		{"K1+O1 = M2", []string{"K1", "O1"}, []string{"M2"}},
		{"K1+P1 = S2", []string{"K1", "P1"}, []string{"S2"}},
		{"K1+Q1 = N2", []string{"K1", "Q1"}, []string{"N2"}},
		{"Sa+Sa = Ssa", []string{"Sa", "Sa"}, []string{constSsa}},
		{"S2+Ssa = K2", []string{"S2", constSsa}, []string{"K2"}},
	}

	for _, at := range times {
		th := absHours(at)
		for _, id := range identities {
			lhs, rhs := 0.0, 0.0
			for _, n := range id.left {
				lhs += nodal.GetEquilibriumArgument(n, th)
			}
			for _, n := range id.right {
				rhs += nodal.GetEquilibriumArgument(n, th)
			}
			if diff := angularDiff(lhs, rhs); diff > 1e-6 {
				t.Errorf("identity %s at %v: lhs=%.9f rhs=%.9f (diff %.9f)", id.desc, at, lhs, rhs, diff)
			}
		}
	}
}

// TestS2PeaksAtNoonAndMidnight verifies the physical anchoring of V: a pure S2
// with Greenwich phase lag 0 and amplitude 1 must peak at (or very close to)
// Greenwich mean noon and midnight, regardless of the reference epoch. The
// only deviation is the tiny S2 nodal phase correction u (|u| < 0.2 deg,
// i.e. the peak may shift by well under a minute).
func TestS2PeaksAtNoonAndMidnight(t *testing.T) {
	params := PredictionParams{
		Constituents: []ConstituentParam{
			{
				Name:          "S2",
				AmplitudeM:    1.0,
				PhaseDeg:      0.0,
				SpeedDegPerHr: 30.0,
			},
		},
		MSL:             0.0,
		NodalCorrection: NewAstronomicalNodalCorrection(),
		ReferenceTime:   time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC),
		PhaseConvention: PhaseConvFESGreenwich,
	}

	day := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	// Direct evaluation: heights at 00:00 and 12:00 UT must be within cos of
	// the small nodal correction u of the full amplitude (f is within 0.3% of
	// 1 for S2), and heights at 06:00/18:00 must be close to the trough.
	for _, hh := range []int{0, 12} {
		h := CalculateTideHeight(day.Add(time.Duration(hh)*time.Hour), params)
		if h < 0.99 {
			t.Errorf("S2 (phase 0) at %02d:00 UT: expected peak (>0.99), got %.6f", hh, h)
		}
	}
	for _, hh := range []int{6, 18} {
		h := CalculateTideHeight(day.Add(time.Duration(hh)*time.Hour), params)
		if h > -0.99 {
			t.Errorf("S2 (phase 0) at %02d:00 UT: expected trough (<-0.99), got %.6f", hh, h)
		}
	}

	// Extrema search: refined high-tide times must fall within 2 minutes of
	// 00:00 and 12:00 UT.
	predictions := GeneratePredictions(day, day.Add(24*time.Hour), time.Minute, params)
	extrema := RefineExtrema(predictions, FindExtrema(predictions))
	if len(extrema.Highs) == 0 {
		t.Fatal("no high tides found for pure S2")
	}
	for _, high := range extrema.Highs {
		utc := high.Time.UTC()
		minutesIntoDay := float64(utc.Hour())*60 + float64(utc.Minute()) + float64(utc.Second())/60
		// Distance to nearest of 0, 720, 1440 minutes.
		nearest := math.Min(
			math.Min(math.Abs(minutesIntoDay-0), math.Abs(minutesIntoDay-720)),
			math.Abs(minutesIntoDay-1440),
		)
		if nearest > 2.0 {
			t.Errorf("S2 high tide at %v: expected within 2 min of 00:00/12:00 UT, off by %.2f min", utc, nearest)
		}
	}
}

// TestEquilibriumArgument_ConsistentAcrossReferenceEpochs verifies that
// evaluating V at a reference epoch and advancing with omega*dt stays very
// close to V evaluated directly at the target time (they agree up to the
// small nonlinear terms of the astronomical polynomials). This confirms the
// tabulated constituent speeds are consistent with the implemented series.
func TestEquilibriumArgument_ConsistentAcrossReferenceEpochs(t *testing.T) {
	nodal := NewAstronomicalNodalCorrection()

	ref := time.Date(2012, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	deltaHours := at.Sub(ref).Hours()

	for name, speed := range StandardConstituents {
		vRef := nodal.GetEquilibriumArgument(name, absHours(ref))
		vAt := nodal.GetEquilibriumArgument(name, absHours(at))
		advanced := vRef + speed*deltaHours
		// Allow modest drift: rounded tabulated speeds (1e-7 deg/hr) over 14
		// years accumulate to ~0.02 deg, plus quadratic polynomial terms.
		if diff := angularDiff(advanced, vAt); diff > 0.5 {
			t.Errorf("V(%s): omega*dt advance from 2012 ref differs from direct evaluation by %.4f deg", name, diff)
		}
	}
}
