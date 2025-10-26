package domain

import "math"

// AstronomicalNodalCorrection implements nodal corrections based on astronomical arguments.
// Based on Schureman (1958) and Foreman (1977).
type AstronomicalNodalCorrection struct {
}

// NewAstronomicalNodalCorrection creates a nodal correction calculator.
func NewAstronomicalNodalCorrection() *AstronomicalNodalCorrection {
	return &AstronomicalNodalCorrection{}
}

// GetFactors returns the nodal correction amplitude factor (f) and phase correction (u) in degrees.
func (n *AstronomicalNodalCorrection) GetFactors(constituent string, t float64) (f, u float64) {
	// Calculate astronomical arguments at time t.
	args := n.calculateAstronomicalArguments(t)

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
func (n *AstronomicalNodalCorrection) getS2Factors(args AstronomicalArguments) (f, u float64) {
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
func (n *AstronomicalNodalCorrection) getP1Factors(args AstronomicalArguments) (f, u float64) {
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
