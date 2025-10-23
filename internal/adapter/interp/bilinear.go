package interp

import (
	"fmt"
	"math"
)

// Point2D represents a 2D coordinate.
type Point2D struct {
	X float64
	Y float64
}

// GridCell represents a cell in a regular grid with four corner values.
type GridCell struct {
	// Corner coordinates (forming a rectangle).
	X0, X1 float64 // X boundaries (e.g., longitude).
	Y0, Y1 float64 // Y boundaries (e.g., latitude).

	// Values at the four corners:
	// V00: value at (X0, Y0).
	// V10: value at (X1, Y0).
	// V01: value at (X0, Y1).
	// V11: value at (X1, Y1).
	V00, V10, V01, V11 float64
}

// BilinearInterpolate performs bilinear interpolation within a grid cell
// Formula:
//
//	f(x,y) ≈ (1-t)(1-u)f(x0,y0) + t(1-u)f(x1,y0) + (1-t)u*f(x0,y1) + tu*f(x1,y1)
//
// where:
//
//	t = (x - x0) / (x1 - x0)
//	u = (y - y0) / (y1 - y0)
func BilinearInterpolate(cell GridCell, x, y float64) (float64, error) {
	// Validate grid cell.
	if cell.X1 <= cell.X0 {
		return 0, fmt.Errorf("invalid grid cell: X1 must be > X0")
	}
	if cell.Y1 <= cell.Y0 {
		return 0, fmt.Errorf("invalid grid cell: Y1 must be > Y0")
	}

	// Check if point is within cell (with small tolerance for floating point).
	const epsilon = 1e-9
	if x < cell.X0-epsilon || x > cell.X1+epsilon {
		return 0, fmt.Errorf("x coordinate %.6f is outside grid cell [%.6f, %.6f]", x, cell.X0, cell.X1)
	}
	if y < cell.Y0-epsilon || y > cell.Y1+epsilon {
		return 0, fmt.Errorf("y coordinate %.6f is outside grid cell [%.6f, %.6f]", y, cell.Y0, cell.Y1)
	}

	// Calculate normalized coordinates (0 to 1).
	t := (x - cell.X0) / (cell.X1 - cell.X0)
	u := (y - cell.Y0) / (cell.Y1 - cell.Y0)

	// Clamp to [0, 1] to handle edge cases with floating point precision.
	t = math.Max(0, math.Min(1, t))
	u = math.Max(0, math.Min(1, u))

	// Bilinear interpolation formula.
	result := (1-t)*(1-u)*cell.V00 +
		t*(1-u)*cell.V10 +
		(1-t)*u*cell.V01 +
		t*u*cell.V11

	return result, nil
}

// Grid2D represents a regular 2D grid for interpolation.
type Grid2D struct {
	X      []float64   // X coordinates (e.g., longitudes).
	Y      []float64   // Y coordinates (e.g., latitudes).
	Values [][]float64 // Values[i][j] corresponds to (X[j], Y[i]).
}

// Validate checks if the grid is valid.
func (g *Grid2D) Validate() error {
	if len(g.X) < 2 {
		return fmt.Errorf("grid must have at least 2 X coordinates")
	}
	if len(g.Y) < 2 {
		return fmt.Errorf("grid must have at least 2 Y coordinates")
	}
	if len(g.Values) != len(g.Y) {
		return fmt.Errorf("number of value rows (%d) must match Y coordinates (%d)", len(g.Values), len(g.Y))
	}

	for i, row := range g.Values {
		if len(row) != len(g.X) {
			return fmt.Errorf("row %d has %d values, expected %d", i, len(row), len(g.X))
		}
	}

	// Check that coordinates are sorted and unique.
	for i := 1; i < len(g.X); i++ {
		if g.X[i] <= g.X[i-1] {
			return fmt.Errorf("X coordinates must be strictly increasing")
		}
	}
	for i := 1; i < len(g.Y); i++ {
		if g.Y[i] <= g.Y[i-1] {
			return fmt.Errorf("Y coordinates must be strictly increasing")
		}
	}

	return nil
}

// InterpolateAt performs bilinear interpolation at a given point.
func (g *Grid2D) InterpolateAt(x, y float64) (float64, error) {
	if err := g.Validate(); err != nil {
		return 0, fmt.Errorf("invalid grid: %w", err)
	}

	// Find the grid cell containing (x, y).
	// Binary search for X.
	xIdx := -1
	for i := 0; i < len(g.X)-1; i++ {
		if x >= g.X[i] && x <= g.X[i+1] {
			xIdx = i
			break
		}
	}
	if xIdx == -1 {
		return 0, fmt.Errorf("x coordinate %.6f is outside grid range [%.6f, %.6f]", x, g.X[0], g.X[len(g.X)-1])
	}

	// Binary search for Y.
	yIdx := -1
	for i := 0; i < len(g.Y)-1; i++ {
		if y >= g.Y[i] && y <= g.Y[i+1] {
			yIdx = i
			break
		}
	}
	if yIdx == -1 {
		return 0, fmt.Errorf("y coordinate %.6f is outside grid range [%.6f, %.6f]", y, g.Y[0], g.Y[len(g.Y)-1])
	}

	// Create grid cell.
	cell := GridCell{
		X0:  g.X[xIdx],
		X1:  g.X[xIdx+1],
		Y0:  g.Y[yIdx],
		Y1:  g.Y[yIdx+1],
		V00: g.Values[yIdx][xIdx],
		V10: g.Values[yIdx][xIdx+1],
		V01: g.Values[yIdx+1][xIdx],
		V11: g.Values[yIdx+1][xIdx+1],
	}

	return BilinearInterpolate(cell, x, y)
}

// InterpolateBoth interpolates two grids (e.g., amplitude and phase) at the same point.
func InterpolateBoth(grid1, grid2 *Grid2D, x, y float64) (float64, float64, error) {
	// Validate that grids have the same coordinates.
	if len(grid1.X) != len(grid2.X) || len(grid1.Y) != len(grid2.Y) {
		return 0, 0, fmt.Errorf("grids must have the same dimensions")
	}

	val1, err := grid1.InterpolateAt(x, y)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to interpolate grid1: %w", err)
	}

	val2, err := grid2.InterpolateAt(x, y)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to interpolate grid2: %w", err)
	}

	return val1, val2, nil
}
