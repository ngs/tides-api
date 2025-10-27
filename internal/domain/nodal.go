package domain

import "math"

// AstronomicalNodalCorrection implements nodal corrections based on astronomical arguments.
// Based on Schureman (1958) and Foreman (1977).
type AstronomicalNodalCorrection struct {
	coeffs *NodalCoeffSet
}

// NewAstronomicalNodalCorrection creates a nodal correction calculator.
func NewAstronomicalNodalCorrection() *AstronomicalNodalCorrection {
	nc := &AstronomicalNodalCorrection{}
	if set, err := LoadNodalCoeffSetFromEnv(); err == nil {
		nc.coeffs = set
	}
	return nc
}

// GetFactors returns the nodal correction amplitude factor (f) and phase correction (u) in degrees.
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

	// Get nodal corrections for each constituent.
	switch constituent {
	case "M2":
		return n.getM2Factors(args)
	case "S2":
		return n.getS2Factors(args)
	case "N2":
		return n.getN2Factors(args)
	case "K2":
		return n.getK2Factors(args)
	case "K1":
		return n.getK1Factors(args)
	case "O1":
		return n.getO1Factors(args)
	case "P1":
		return n.getP1Factors(args)
	case "Q1":
		return n.getQ1Factors(args)
	default:
		// For unknown constituents, return identity (no correction).
		return 1.0, 0.0
	}
}

// GetEquilibriumArgument returns an approximate equilibrium argument V (degrees)
// for the given constituent at time t (hours since Unix epoch).
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

// Built-in coefficients for major constituents (pyTMD-derived; N in radians).
//
//nolint:gochecknoglobals // Intentional: Read-only constant map for nodal corrections.
var builtInNonlinearCoeffs = map[string]nonlinearCoeff{
	// M2: Principal lunar semidiurnal
	"M2": {term1Sin: map[int]float64{1: -0.03731, 2: 0.00052}, term2Const: 1.0, term2Cos: map[int]float64{1: -0.03731, 2: 0.00052}},
	// S2: Principal solar semidiurnal (very small nodal effect)
	"S2": {term1Sin: map[int]float64{1: 0.00225}, term2Const: 1.0, term2Cos: map[int]float64{1: 0.00225}},
	// N2: Lunar elliptical semidiurnal (similar pattern to M2 per provided table)
	"N2": {term1Sin: map[int]float64{1: -0.03731, 2: 0.00052}, term2Const: 1.0, term2Cos: map[int]float64{1: -0.03731, 2: 0.00052}},
	// K2: Lunisolar semidiurnal
	"K2": {term1Sin: map[int]float64{1: -0.3108, 2: -0.0324}, term2Const: 1.0, term2Cos: map[int]float64{1: 0.2852, 2: 0.0324}},
	// K1: Lunisolar diurnal
	"K1": {term1Sin: map[int]float64{1: -0.1554, 2: 0.0029}, term2Const: 1.0, term2Cos: map[int]float64{1: 0.1158, 2: -0.0029}},
	// O1: Principal lunar diurnal
	"O1": {term1Sin: map[int]float64{1: 0.189, 2: -0.0058}, term2Const: 1.0, term2Cos: map[int]float64{1: 0.189, 2: -0.0058}},
	// P1: Principal solar diurnal
	"P1": {term1Sin: map[int]float64{1: -0.0112}, term2Const: 1.0, term2Cos: map[int]float64{1: -0.0112}},
	// Q1: Lunar elliptical diurnal
	"Q1": {term1Sin: map[int]float64{1: 0.1886}, term2Const: 1.0, term2Cos: map[int]float64{1: 0.1886}},
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

// calculateAstronomicalArguments computes astronomical arguments at time t (hours since epoch).
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

// getM2Factors returns nodal factors for M2 (principal lunar semidiurnal).
func (n *AstronomicalNodalCorrection) getM2Factors(args AstronomicalArguments) (f, u float64) {
	// M2 nodal corrections (Schureman Table 14).
	// Temporarily disable amplitude correction to test phase correction only.
	sinI := math.Sin(Deg2Rad(args.I))

	f = 1.0                // Temporarily disabled.
	u = -2.1 * sinI * sinI // Degrees.

	return f, u
}

// getS2Factors returns nodal factors for S2 (principal solar semidiurnal).
func (n *AstronomicalNodalCorrection) getS2Factors(_ AstronomicalArguments) (f, u float64) {
	// S2 has no nodal correction (solar constituent).
	return 1.0, 0.0
}

// getN2Factors returns nodal factors for N2 (larger lunar elliptic semidiurnal).
func (n *AstronomicalNodalCorrection) getN2Factors(args AstronomicalArguments) (f, u float64) {
	// N2 nodal corrections.
	// Temporarily disable amplitude correction to test phase correction only.
	sinI := math.Sin(Deg2Rad(args.I))

	f = 1.0                // Temporarily disabled.
	u = -2.1 * sinI * sinI // Degrees.

	return f, u
}

// getK2Factors returns nodal factors for K2 (lunisolar semidiurnal).
func (n *AstronomicalNodalCorrection) getK2Factors(args AstronomicalArguments) (f, u float64) {
	// K2 nodal corrections.
	// Temporarily disable amplitude correction to test phase correction only.
	sin2I := math.Sin(2.0 * Deg2Rad(args.I))

	f = 1.0 // Temporarily disabled.
	u = math.Atan2(0.1689*sin2I, 0.2523+0.1689*math.Cos(Deg2Rad(args.I)))
	u = Rad2Deg(u) // Convert to degrees.

	return f, u
}

// getK1Factors returns nodal factors for K1 (lunisolar diurnal).
func (n *AstronomicalNodalCorrection) getK1Factors(args AstronomicalArguments) (f, u float64) {
	// K1 nodal corrections (Schureman Table 14).
	// Temporarily disable amplitude correction to test phase correction only.
	sinNu := math.Sin(Deg2Rad(args.nu))
	sin2Nu := math.Sin(2.0 * Deg2Rad(args.nu))

	f = 1.0                       // Temporarily disabled.
	u = -8.86*sinNu + 0.68*sin2Nu // Degrees.

	return f, u
}

// getO1Factors returns nodal factors for O1 (lunar diurnal).
func (n *AstronomicalNodalCorrection) getO1Factors(args AstronomicalArguments) (f, u float64) {
	// O1 nodal corrections (Schureman Table 14).
	// Temporarily disable amplitude correction to test phase correction only.
	sinNu := math.Sin(Deg2Rad(args.nu))
	sin2Nu := math.Sin(2.0 * Deg2Rad(args.nu))

	f = 1.0                     // Temporarily disabled.
	u = 10.8*sinNu - 1.3*sin2Nu // Degrees.

	return f, u
}

// getP1Factors returns nodal factors for P1 (solar diurnal).
func (n *AstronomicalNodalCorrection) getP1Factors(_ AstronomicalArguments) (f, u float64) {
	// P1 has no nodal correction (solar constituent).
	return 1.0, 0.0
}

// getQ1Factors returns nodal factors for Q1 (larger lunar elliptic diurnal).
func (n *AstronomicalNodalCorrection) getQ1Factors(args AstronomicalArguments) (f, u float64) {
	// Q1 nodal corrections.
	// Temporarily disable amplitude correction to test phase correction only.
	sinNu := math.Sin(Deg2Rad(args.nu))
	sin2Nu := math.Sin(2.0 * Deg2Rad(args.nu))

	f = 1.0                     // Temporarily disabled.
	u = 10.8*sinNu - 1.3*sin2Nu // Degrees.

	return f, u
}
