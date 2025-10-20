package interp

import (
	"math"
	"testing"
)

// TestBilinearInterpolate_CenterPoint tests interpolation at the center of a grid cell
func TestBilinearInterpolate_CenterPoint(t *testing.T) {
	cell := GridCell{
		X0: 0.0, X1: 2.0,
		Y0: 0.0, Y1: 2.0,
		V00: 1.0, V10: 3.0,
		V01: 5.0, V11: 7.0,
	}

	// At center (1.0, 1.0), t=0.5, u=0.5
	// Result = 0.5*0.5*1 + 0.5*0.5*3 + 0.5*0.5*5 + 0.5*0.5*7
	//        = 0.25 * (1 + 3 + 5 + 7) = 0.25 * 16 = 4.0
	result, err := BilinearInterpolate(cell, 1.0, 1.0)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	expected := 4.0
	if math.Abs(result-expected) > 1e-9 {
		t.Errorf("Center point: expected %.10f, got %.10f", expected, result)
	}
}

// TestBilinearInterpolate_CornerPoints tests that corners return exact values
func TestBilinearInterpolate_CornerPoints(t *testing.T) {
	cell := GridCell{
		X0: 0.0, X1: 10.0,
		Y0: 0.0, Y1: 10.0,
		V00: 1.0, V10: 2.0,
		V01: 3.0, V11: 4.0,
	}

	tests := []struct {
		x, y     float64
		expected float64
		name     string
	}{
		{0.0, 0.0, 1.0, "bottom-left"},
		{10.0, 0.0, 2.0, "bottom-right"},
		{0.0, 10.0, 3.0, "top-left"},
		{10.0, 10.0, 4.0, "top-right"},
	}

	for _, tt := range tests {
		result, err := BilinearInterpolate(cell, tt.x, tt.y)
		if err != nil {
			t.Fatalf("Unexpected error for %s: %v", tt.name, err)
		}

		if math.Abs(result-tt.expected) > 1e-9 {
			t.Errorf("%s corner: expected %.10f, got %.10f", tt.name, tt.expected, result)
		}
	}
}

// TestBilinearInterpolate_LinearCase tests a perfectly linear case
func TestBilinearInterpolate_LinearCase(t *testing.T) {
	// Create a grid where values increase linearly in x
	// V = x (independent of y)
	cell := GridCell{
		X0: 0.0, X1: 10.0,
		Y0: 0.0, Y1: 10.0,
		V00: 0.0, V10: 10.0,
		V01: 0.0, V11: 10.0,
	}

	// Test at x=5, should get value 5.0 regardless of y
	tests := []struct {
		x, y     float64
		expected float64
	}{
		{5.0, 0.0, 5.0},
		{5.0, 5.0, 5.0},
		{5.0, 10.0, 5.0},
		{2.5, 7.0, 2.5},
	}

	for _, tt := range tests {
		result, err := BilinearInterpolate(cell, tt.x, tt.y)
		if err != nil {
			t.Fatalf("Unexpected error at (%.1f, %.1f): %v", tt.x, tt.y, err)
		}

		if math.Abs(result-tt.expected) > 1e-9 {
			t.Errorf("At (%.1f, %.1f): expected %.10f, got %.10f", tt.x, tt.y, tt.expected, result)
		}
	}
}

// TestBilinearInterpolate_OutOfBounds tests error handling for out-of-bounds points
func TestBilinearInterpolate_OutOfBounds(t *testing.T) {
	cell := GridCell{
		X0: 0.0, X1: 10.0,
		Y0: 0.0, Y1: 10.0,
		V00: 1.0, V10: 2.0,
		V01: 3.0, V11: 4.0,
	}

	tests := []struct {
		x, y float64
		name string
	}{
		{-1.0, 5.0, "x too small"},
		{11.0, 5.0, "x too large"},
		{5.0, -1.0, "y too small"},
		{5.0, 11.0, "y too large"},
	}

	for _, tt := range tests {
		_, err := BilinearInterpolate(cell, tt.x, tt.y)
		if err == nil {
			t.Errorf("%s: expected error for point (%.1f, %.1f), got nil", tt.name, tt.x, tt.y)
		}
	}
}

// TestGrid2D_InterpolateAt tests 2D grid interpolation
func TestGrid2D_InterpolateAt(t *testing.T) {
	// Create a simple 3x3 grid
	grid := &Grid2D{
		X: []float64{0.0, 1.0, 2.0},
		Y: []float64{0.0, 1.0, 2.0},
		Values: [][]float64{
			{1.0, 2.0, 3.0}, // y=0
			{4.0, 5.0, 6.0}, // y=1
			{7.0, 8.0, 9.0}, // y=2
		},
	}

	// Test at grid points (should return exact values)
	tests := []struct {
		x, y     float64
		expected float64
	}{
		{0.0, 0.0, 1.0},
		{1.0, 0.0, 2.0},
		{2.0, 0.0, 3.0},
		{0.0, 1.0, 4.0},
		{1.0, 1.0, 5.0},
		{2.0, 2.0, 9.0},
	}

	for _, tt := range tests {
		result, err := grid.InterpolateAt(tt.x, tt.y)
		if err != nil {
			t.Fatalf("Unexpected error at (%.1f, %.1f): %v", tt.x, tt.y, err)
		}

		if math.Abs(result-tt.expected) > 1e-9 {
			t.Errorf("At (%.1f, %.1f): expected %.10f, got %.10f", tt.x, tt.y, tt.expected, result)
		}
	}

	// Test interpolation at midpoint
	// Between (0,0)=1, (1,0)=2, (0,1)=4, (1,1)=5
	// At (0.5, 0.5) should be average = 3.0
	result, err := grid.InterpolateAt(0.5, 0.5)
	if err != nil {
		t.Fatalf("Unexpected error at midpoint: %v", err)
	}

	expected := 3.0
	if math.Abs(result-expected) > 1e-9 {
		t.Errorf("Midpoint (0.5, 0.5): expected %.10f, got %.10f", expected, result)
	}
}

// TestGrid2D_Validate tests grid validation
func TestGrid2D_Validate(t *testing.T) {
	tests := []struct {
		name    string
		grid    *Grid2D
		wantErr bool
	}{
		{
			name: "valid grid",
			grid: &Grid2D{
				X:      []float64{0.0, 1.0, 2.0},
				Y:      []float64{0.0, 1.0},
				Values: [][]float64{{1, 2, 3}, {4, 5, 6}},
			},
			wantErr: false,
		},
		{
			name: "too few X coords",
			grid: &Grid2D{
				X:      []float64{0.0},
				Y:      []float64{0.0, 1.0},
				Values: [][]float64{{1}, {2}},
			},
			wantErr: true,
		},
		{
			name: "mismatched row count",
			grid: &Grid2D{
				X:      []float64{0.0, 1.0},
				Y:      []float64{0.0, 1.0},
				Values: [][]float64{{1, 2}}, // Only 1 row, expected 2
			},
			wantErr: true,
		},
		{
			name: "mismatched column count",
			grid: &Grid2D{
				X:      []float64{0.0, 1.0, 2.0},
				Y:      []float64{0.0, 1.0},
				Values: [][]float64{{1, 2}, {3, 4}}, // 2 columns, expected 3
			},
			wantErr: true,
		},
		{
			name: "non-increasing X",
			grid: &Grid2D{
				X:      []float64{0.0, 2.0, 1.0},
				Y:      []float64{0.0, 1.0},
				Values: [][]float64{{1, 2, 3}, {4, 5, 6}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.grid.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
