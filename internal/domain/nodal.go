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

// GetEquilibriumArgument returns an approximate equilibrium argument V (degrees)
// for the given constituent at absolute time t (hours since Unix epoch).
// Placeholder returns 0 until the full astronomical series is integrated.
func (n *AstronomicalNodalCorrection) GetEquilibriumArgument(constituent string, _ float64) float64 {
	if n.coeffs != nil {
		if c, ok := n.coeffs.ByName[constituent]; ok {
			return c.V0
		}
	}
	return 0.0
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

	// p: Mean longitude of lunar perigee.
	p := 83.35324 + 4069.01363*T - 0.0103238*T*T - T*T*T/80053.0

	// ps: Mean longitude of solar perigee (perihelion).
	ps := 282.94 + 1.7192*T

	// Normalize to [0, 360) degrees.
	N = math.Mod(N, 360.0)
	if N < 0 {
		N += 360.0
	}
	p = math.Mod(p, 360.0)
	if p < 0 {
		p += 360.0
	}
	ps = math.Mod(ps, 360.0)
	if ps < 0 {
		ps += 360.0
	}

	// Calculate inclination of lunar orbit.
	I := math.Acos(0.91370 - 0.03569*math.Cos(Deg2Rad(N)))
	IDeg := Rad2Deg(I)

	// Calculate nutation factor (nu) and xi.
	nu := math.Asin(0.08978 * math.Sin(Deg2Rad(N)) / math.Sin(I))
	nuDeg := Rad2Deg(nu)

	xi := N - 2.0*nuDeg

	return AstronomicalArguments{
		N:  N,
		p:  p,
		ps: ps,
		I:  IDeg,
		nu: nuDeg,
		xi: xi,
	}
}
