package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
)

// NodalCoeff holds Fourier series coefficients in N (degrees) for f and u,
// and an optional constant V0 (degrees) for the equilibrium argument.
// f(N) = F0 + sum_k FCos[k]*cos(kN) + sum_k FSin[k]*sin(kN)
// u(N) = U0 + sum_k UCos[k]*cos(kN) + sum_k USin[k]*sin(kN)
type NodalCoeff struct {
	Name string  `json:"name"`
	F0   float64 `json:"f0"`
	U0   float64 `json:"u0"`
	V0   float64 `json:"v0"`

	FCos map[string]float64 `json:"f_cos,omitempty"`
	FSin map[string]float64 `json:"f_sin,omitempty"`
	UCos map[string]float64 `json:"u_cos,omitempty"`
	USin map[string]float64 `json:"u_sin,omitempty"`

	Nonlinear *NonlinearSpec `json:"_nonlinear,omitempty"`
}

// EvalF evaluates the nodal amplitude factor f at a given lunar node angle N (degrees).
func (c *NodalCoeff) EvalF(Ndeg float64) float64 {
	f := c.F0
	for k, a := range c.FCos {
		ki, _ := strconv.Atoi(k)
		f += a * mathCos(Deg2Rad(float64(ki)*Ndeg))
	}
	for k, b := range c.FSin {
		ki, _ := strconv.Atoi(k)
		f += b * mathSin(Deg2Rad(float64(ki)*Ndeg))
	}
	if f == 0 {
		f = 1
	}
	return f
}

// EvalU evaluates the nodal phase correction u at a given lunar node angle N (degrees).
func (c *NodalCoeff) EvalU(Ndeg float64) float64 {
	u := c.U0
	for k, a := range c.UCos {
		ki, _ := strconv.Atoi(k)
		u += a * mathCos(Deg2Rad(float64(ki)*Ndeg))
	}
	for k, b := range c.USin {
		ki, _ := strconv.Atoi(k)
		u += b * mathSin(Deg2Rad(float64(ki)*Ndeg))
	}
	return u
}

// EvalNonlinear evaluates the nonlinear (sqrt/atan2) specification if present.
func (c *NodalCoeff) EvalNonlinear(Ndeg float64) (float64, float64, bool) {
	if c.Nonlinear == nil {
		return 0, 0, false
	}
	Nrad := Deg2Rad(Ndeg)
	term1 := 0.0
	for k, coeff := range c.Nonlinear.Term1Sin {
		ki, _ := strconv.Atoi(k)
		term1 += coeff * mathSin(float64(ki)*Nrad)
	}
	term2 := c.Nonlinear.Term2Const
	for k, coeff := range c.Nonlinear.Term2Cos {
		ki, _ := strconv.Atoi(k)
		term2 += coeff * mathCos(float64(ki)*Nrad)
	}
	f := math.Sqrt(term1*term1 + term2*term2)
	u := Rad2Deg(math.Atan2(term1, term2))
	return f, u, true
}

// NonlinearSpec specifies nonlinear nodal correction coefficients.
type NonlinearSpec struct {
	Term1Sin   map[string]float64 `json:"term1_sin,omitempty"`
	Term2Const float64            `json:"term2_const"`
	Term2Cos   map[string]float64 `json:"term2_cos,omitempty"`
}

// NodalCoeffSet contains a set of nodal coefficients for multiple constituents.
type NodalCoeffSet struct {
	Coeffs []NodalCoeff          `json:"coeffs"`
	ByName map[string]NodalCoeff `json:"-"`
}

// LoadNodalCoeffSet loads nodal coefficients from a JSON file.
func LoadNodalCoeffSet(path string) (*NodalCoeffSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var set NodalCoeffSet
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("invalid nodal coeff json: %w", err)
	}
	set.ByName = make(map[string]NodalCoeff)
	for _, c := range set.Coeffs {
		set.ByName[c.Name] = c
	}
	return &set, nil
}

// LoadNodalCoeffSetFromEnv loads nodal coefficients from the path specified in ASTRO_COEFFS_PATH env var.
func LoadNodalCoeffSetFromEnv() (*NodalCoeffSet, error) {
	path := os.Getenv("ASTRO_COEFFS_PATH")
	if path == "" {
		// Try default path
		path = "data/astro_coeffs.json"
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return LoadNodalCoeffSet(path)
}

// Local wrappers to avoid importing math here repeatedly.
func mathCos(x float64) float64 { return math.Cos(x) }
func mathSin(x float64) float64 { return math.Sin(x) }
