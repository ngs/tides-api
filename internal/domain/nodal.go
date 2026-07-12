package domain

import (
	"log"
	"math"
	"os"
	"sync"
)

// AstronomicalNodalCorrection implements nodal corrections based on astronomical arguments.
// Based on Schureman (1958) and Foreman (1977).
type AstronomicalNodalCorrection struct {
	coeffs *NodalCoeffSet
}

//nolint:gochecknoglobals // Intentional: sync.Once pattern for lazy loading.
var (
	nodalCoeffsOnce sync.Once
	nodalCoeffsSet  *NodalCoeffSet
)

// NewAstronomicalNodalCorrection creates a nodal correction calculator.
// The coefficient file (ASTRO_COEFFS_PATH) is loaded once per process and cached;
// load failures are logged instead of being silently discarded. A missing file
// is not an error: the built-in coefficients are used instead.
func NewAstronomicalNodalCorrection() *AstronomicalNodalCorrection {
	nodalCoeffsOnce.Do(func() {
		set, err := LoadNodalCoeffSetFromEnv()
		if err != nil {
			if os.IsNotExist(err) {
				// No external coefficient file: fall back to built-in coefficients.
				return
			}
			log.Printf("Warning: failed to load nodal coefficients: %v", err)
			return
		}
		nodalCoeffsSet = set
	})
	return &AstronomicalNodalCorrection{coeffs: nodalCoeffsSet}
}

// GetFactors returns the nodal correction amplitude factor (f) and phase correction (u)
// in degrees at absolute time t (hours since Unix epoch).
func (n *AstronomicalNodalCorrection) GetFactors(constituent string, t float64) (f, u float64) {
	// Calculate astronomical arguments at time t.
	args := n.calculateAstronomicalArguments(t)

	// Use external coefficients if available (Fourier series in N).
	//nolint:nestif // Nodal correction logic with fallback handling.
	if n.coeffs != nil {
		if c, ok := n.coeffs.ByName[constituent]; ok {
			N := args.N
			if nf, nu, ok := c.EvalNonlinear(N); ok {
				if nf == 0 {
					nf = 1
				}
				return nf, nu
			}
			f = c.EvalF(N)
			u = c.EvalU(N)
			if f == 0 {
				f = 1
			}
			return f, u
		}
	}

	// Use built-in nonlinear coefficients (pyTMD-derived) if available.
	if coeff, ok := builtInNonlinearCoeffs[constituent]; ok {
		Nrad := Deg2Rad(args.N)
		// term1 = sum a_k sin(kN), term2 = b0 + sum b_k cos(kN)
		term1 := 0.0
		for k, a := range coeff.term1Sin {
			term1 += a * math.Sin(float64(k)*Nrad)
		}
		term2 := coeff.term2Const
		for k, b := range coeff.term2Cos {
			term2 += b * math.Cos(float64(k)*Nrad)
		}
		f = math.Sqrt(term1*term1 + term2*term2)
		u = Rad2Deg(math.Atan2(term1, term2))
		return f, u
	}

	// For constituents without coefficients, return identity (no correction).
	return 1.0, 0.0
}

// GetEquilibriumArgument returns the Greenwich equilibrium argument V (degrees,
// normalized to [0, 360)) for the given constituent at absolute time t (hours
// since Unix epoch).
//
// V is computed analytically from the fundamental astronomical arguments
// (T, s, h, p) following Schureman (1958) Table 2 (see equilibriumArgumentDeg).
// The analytic series takes precedence over any V0 constant in an external
// coefficient file: the shipped data/astro_coeffs.json carries v0=0 for every
// constituent (it predates this implementation), so honoring those values
// would silently zero out V. A coefficient-file V0 is used only as a fallback
// for constituents that have no analytic series here, which lets external
// files supply V for exotic constituents without code changes.
func (n *AstronomicalNodalCorrection) GetEquilibriumArgument(constituent string, t float64) float64 {
	args := n.calculateAstronomicalArguments(t)

	// T: hour angle of the mean sun at Greenwich (degrees). The mean sun
	// crosses the lower meridian (hour angle 180 deg) at 00:00 UT, so
	// T = 180 + 15 * (UT hours of day), written here as a continuous
	// function of days since the Unix epoch (which is at 00:00 UT).
	days := t / 24.0
	hourAngleT := 180.0 + 360.0*(days-math.Floor(days))

	if v, ok := equilibriumArgumentDeg(constituent, hourAngleT, args.s, args.h, args.p); ok {
		return normalizeDeg(v)
	}

	// Fallback: constant V0 from an external coefficient file, if present.
	if n.coeffs != nil {
		if c, ok := n.coeffs.ByName[constituent]; ok {
			return normalizeDeg(c.V0)
		}
	}
	return 0.0
}

// equilibriumArgumentDeg returns the Greenwich equilibrium argument V (degrees,
// not normalized) for a constituent given the fundamental arguments:
//
//	T: hour angle of the mean sun at Greenwich (180 deg at 00:00 UT)
//	s: mean longitude of the Moon
//	h: mean longitude of the Sun
//	p: mean longitude of the lunar perigee
//
// The series follows Schureman (1958) Table 2, which is also the convention
// used by NOAA, xtide, and pyTMD/arguments.py (Ray 1999 ARGUMENTS.f; pyTMD
// writes t1 = 15*hour instead of T = 180 + 15*hour, which is identical mod
// 360 once the explicit +-90/+270 offsets are compared consistently).
//
// Sign convention for the +-90 deg terms: literature listings disagree on the
// signs for the diurnal species, so we follow Schureman Table 2 exactly:
//
//	K1: T + h - 90    O1: T - 2s + h + 90    P1: T - h + 90    Q1: T - 3s + h + p + 90
//
// This choice is self-consistent with the equilibrium tide physics: the
// diurnal pairs recombine into the semidiurnal parents with the 90 deg
// offsets cancelling exactly,
//
//	V(K1) + V(O1) = V(M2),  V(K1) + V(P1) = V(S2),  V(K1) + V(Q1) = V(N2),
//
// which is required because (K1, O1) both arise from splitting the lunar
// declinational potential and (K1, P1) from the solar one. It also makes a
// pure S2 (Greenwich phase lag 0) peak at Greenwich mean noon and midnight,
// as the equilibrium solar semidiurnal tide must (verified in tests).
// Shallow-water/compound constituents use sums of their parents' arguments.
//
// The second return value is false for constituents without a known series.
func equilibriumArgumentDeg(constituent string, tHr, s, h, p float64) (float64, bool) {
	vM2 := 2*tHr - 2*s + 2*h
	vS2 := 2 * tHr
	vN2 := 2*tHr - 3*s + 2*h + p
	vK1 := tHr + h - 90.0

	switch constituent {
	// Semidiurnal.
	case "M2":
		return vM2, true
	case "S2":
		return vS2, true
	case "N2":
		return vN2, true
	case "K2":
		return 2*tHr + 2*h, true
	// Diurnal.
	case "K1":
		return vK1, true
	case "O1":
		return tHr - 2*s + h + 90.0, true
	case "P1":
		return tHr - h + 90.0, true
	case "Q1":
		return tHr - 3*s + h + p + 90.0, true
	// Shallow water (overtides and compound tides of the parents above).
	case "M4":
		return 2 * vM2, true
	case "M6":
		return 3 * vM2, true
	case "S4":
		return 2 * vS2, true
	case constMN4:
		return vM2 + vN2, true
	case constMS4:
		return vM2 + vS2, true
	case constMK3:
		return vM2 + vK1, true
	// Long period.
	case "Mf":
		return 2 * s, true
	case "Mm":
		return s - p, true
	case constSsa:
		return 2 * h, true
	case "Sa":
		return h, true
	}
	return 0, false
}

// normalizeDeg normalizes an angle in degrees to [0, 360).
func normalizeDeg(deg float64) float64 {
	deg = math.Mod(deg, 360.0)
	if deg < 0 {
		deg += 360.0
	}
	return deg
}

// Nonlinear nodal coefficients structure: f,u computed via sqrt/atan2 of sin/cos series in N (radians).
type nonlinearCoeff struct {
	term1Sin   map[int]float64 // a_k for sin(kN)
	term2Const float64         // b0
	term2Cos   map[int]float64 // b_k for cos(kN)
}

// Shared coefficients for constituents with identical sin/cos terms.
//
//nolint:gochecknoglobals // Intentional: Read-only constant maps for nodal corrections.
var (
	m2SinCosCoeffs = map[int]float64{1: -0.03731, 2: 0.00052}
	s2SinCosCoeffs = map[int]float64{1: 0.00225}
	n2SinCosCoeffs = map[int]float64{1: -0.03731, 2: 0.00052}
	o1SinCosCoeffs = map[int]float64{1: 0.189, 2: -0.0058}
	p1SinCosCoeffs = map[int]float64{1: -0.0112}
	q1SinCosCoeffs = map[int]float64{1: 0.1886}
)

// Built-in coefficients for major constituents (pyTMD-derived; N in radians).
//
//nolint:gochecknoglobals // Intentional: Read-only constant map for nodal corrections.
var builtInNonlinearCoeffs = map[string]nonlinearCoeff{
	// M2: Principal lunar semidiurnal
	"M2": {term1Sin: m2SinCosCoeffs, term2Const: 1.0, term2Cos: m2SinCosCoeffs},
	// S2: Principal solar semidiurnal (very small nodal effect)
	"S2": {term1Sin: s2SinCosCoeffs, term2Const: 1.0, term2Cos: s2SinCosCoeffs},
	// N2: Lunar elliptical semidiurnal (similar pattern to M2 per provided table)
	"N2": {term1Sin: n2SinCosCoeffs, term2Const: 1.0, term2Cos: n2SinCosCoeffs},
	// K2: Lunisolar semidiurnal
	"K2": {term1Sin: map[int]float64{1: -0.3108, 2: -0.0324}, term2Const: 1.0, term2Cos: map[int]float64{1: 0.2852, 2: 0.0324}},
	// K1: Lunisolar diurnal
	"K1": {term1Sin: map[int]float64{1: -0.1554, 2: 0.0029}, term2Const: 1.0, term2Cos: map[int]float64{1: 0.1158, 2: -0.0029}},
	// O1: Principal lunar diurnal
	"O1": {term1Sin: o1SinCosCoeffs, term2Const: 1.0, term2Cos: o1SinCosCoeffs},
	// P1: Principal solar diurnal
	"P1": {term1Sin: p1SinCosCoeffs, term2Const: 1.0, term2Cos: p1SinCosCoeffs},
	// Q1: Lunar elliptical diurnal
	"Q1": {term1Sin: q1SinCosCoeffs, term2Const: 1.0, term2Cos: q1SinCosCoeffs},
}

// AstronomicalArguments holds the fundamental astronomical arguments.
type AstronomicalArguments struct {
	N  float64 // Mean longitude of lunar ascending node (degrees).
	s  float64 // Mean longitude of the Moon (degrees).
	h  float64 // Mean longitude of the Sun (degrees).
	p  float64 // Mean longitude of lunar perigee (degrees).
	ps float64 // Mean longitude of solar perigee (degrees).
	I  float64 // Inclination of lunar orbit (degrees).
	nu float64 // Nutation in longitude (degrees).
	xi float64 // Nutation factor.
}

// calculateAstronomicalArguments computes astronomical arguments at absolute time t
// (hours since Unix epoch).
// Based on Schureman (1958) formulas.
func (n *AstronomicalNodalCorrection) calculateAstronomicalArguments(t float64) AstronomicalArguments {
	// Convert hours to days since epoch (J2000.0 = 2000-01-01 12:00:00 UTC).
	// Unix epoch (1970-01-01 00:00:00) is 10957.5 days before J2000.0.
	daysFromUnix := t / 24.0
	daysFromJ2000 := daysFromUnix - 10957.5

	// Convert to Julian centuries from J2000.0.
	T := daysFromJ2000 / 36525.0

	// Calculate fundamental astronomical arguments (Schureman, 1958).
	// N: Mean longitude of lunar ascending node.
	N := 125.04452 - 1934.136261*T + 0.0020708*T*T + T*T*T/450000.0

	// s: Mean longitude of the Moon (Meeus/IERS; linear rate 0.5490165 deg/hr,
	// consistent with the tabulated constituent speeds).
	s := 218.3164477 + 481267.88123421*T - 0.0015786*T*T + T*T*T/538841.0

	// h: Mean longitude of the Sun (linear rate 0.0410686 deg/hr).
	h := 280.46646 + 36000.76983*T + 0.0003032*T*T

	// p: Mean longitude of lunar perigee.
	p := 83.35324 + 4069.01363*T - 0.0103238*T*T - T*T*T/80053.0

	// ps: Mean longitude of solar perigee (perihelion).
	ps := 282.94 + 1.7192*T

	// Normalize to [0, 360) degrees.
	N = normalizeDeg(N)
	s = normalizeDeg(s)
	h = normalizeDeg(h)
	p = normalizeDeg(p)
	ps = normalizeDeg(ps)

	// Calculate inclination of lunar orbit.
	I := math.Acos(0.91370 - 0.03569*math.Cos(Deg2Rad(N)))
	IDeg := Rad2Deg(I)

	// Calculate nutation factor (nu) and xi.
	nu := math.Asin(0.08978 * math.Sin(Deg2Rad(N)) / math.Sin(I))
	nuDeg := Rad2Deg(nu)

	xi := N - 2.0*nuDeg

	return AstronomicalArguments{
		N:  N,
		s:  s,
		h:  h,
		p:  p,
		ps: ps,
		I:  IDeg,
		nu: nuDeg,
		xi: xi,
	}
}
