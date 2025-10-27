package domain

import "math"

// Constituent represents a tidal constituent with its angular speed.
type Constituent struct {
	Name          string  // E.g., "M2", "S2", "K1", "O1".
	SpeedDegPerHr float64 // Angular speed in degrees per hour.
}

// ConstituentParam holds the amplitude and phase for a specific location.
type ConstituentParam struct {
	Name          string
	AmplitudeM    float64 // Amplitude in meters.
	PhaseDeg      float64 // Phase in degrees.
	SpeedDegPerHr float64 // Angular speed in degrees per hour.
}

// StandardConstituents contains tidal constituents with their angular speeds (deg/hour).
// Reference: https://www.pmel.noaa.gov/pubs/PDF/park2589/park2589.pdf
var StandardConstituents = map[string]float64{
	// Principal lunar semidiurnal.
	"M2": 28.9841042,
	// Principal solar semidiurnal.
	"S2": 30.0000000,
	// Larger lunar elliptic semidiurnal.
	"N2": 28.4397295,
	// Lunisolar semidiurnal.
	"K2": 30.0821373,

	// Lunar diurnal.
	"K1": 15.0410686,
	// Lunar diurnal.
	"O1": 13.9430356,
	// Solar diurnal.
	"P1": 14.9589314,
	// Solar diurnal.
	"Q1": 13.3986609,

	// Shallow water constituents.
	"M4":  57.9682084,
	"M6":  86.9523127,
	"MK3": 44.0251729,
	"S4":  60.0000000,
	"MN4": 57.4238337,
	"MS4": 58.9841042,

	// Long period.
	"Mf":  1.0980331,
	"Mm":  0.5443747,
	"Ssa": 0.0821373,
	"Sa":  0.0410686,
}

// NodalCorrection is an interface for applying nodal corrections.
// MVP: returns identity (1.0, 0.0).
type NodalCorrection interface {
    // GetFactors returns the amplitude factor (f) and phase correction (u) in degrees.
    GetFactors(constituent string, t float64) (f float64, u float64)
    // GetEquilibriumArgument returns the equilibrium argument V (degrees) for the constituent.
    // V accounts for slowly varying astronomical arguments (Schureman/Foreman).
    // Implementations may return 0 if not available.
    GetEquilibriumArgument(constituent string, t float64) float64
}

// IdentityNodalCorrection is a dummy implementation that returns no correction.
type IdentityNodalCorrection struct{}

// GetFactors returns the nodal correction factors (no correction for identity).
func (i *IdentityNodalCorrection) GetFactors(_ string, _ float64) (float64, float64) {
    return 1.0, 0.0
}

// GetEquilibriumArgument returns the equilibrium argument (no correction for identity).
func (i *IdentityNodalCorrection) GetEquilibriumArgument(_ string, _ float64) float64 {
    return 0.0
}

// GetConstituentSpeed returns the angular speed for a given constituent name.
func GetConstituentSpeed(name string) (float64, bool) {
	speed, ok := StandardConstituents[name]
	return speed, ok
}

// GetAllConstituents returns a slice of all standard constituents.
func GetAllConstituents() []Constituent {
	constituents := make([]Constituent, 0, len(StandardConstituents))
	for name, speed := range StandardConstituents {
		constituents = append(constituents, Constituent{
			Name:          name,
			SpeedDegPerHr: speed,
		})
	}
	return constituents
}

// Deg2Rad converts degrees to radians.
func Deg2Rad(deg float64) float64 {
	return deg * math.Pi / 180.0
}

// Rad2Deg converts radians to degrees.
func Rad2Deg(rad float64) float64 {
	return rad * 180.0 / math.Pi
}
