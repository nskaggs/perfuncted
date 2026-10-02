package window

import (
	"errors"
	"math"
	"testing"
)

// A non-positive edge is the request that reaches a compositor as a nonsense
// size, so it must never be accepted.
func TestValidateSizeRejectsNonPositiveDimensions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{name: "zero width", width: 0, height: 100},
		{name: "zero height", width: 100, height: 0},
		{name: "negative width", width: -1, height: 100},
		{name: "negative height", width: 100, height: -100},
		{name: "both negative", width: -1, height: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSize(tc.width, tc.height)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ValidateSize(%d, %d) = %v, want ErrInvalidArgument", tc.width, tc.height, err)
			}
		})
	}
}

// Values beyond the 16-bit geometry space are truncated by the protocol rather
// than rejected, so they must be refused before dispatch. -1 is the case that
// turns into a 65535-pixel edge.
func TestValidateSizeRejectsUnrepresentableDimensions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{name: "negative width", width: -1, height: 100},
		{name: "negative height", width: 100, height: -1},
		{name: "width above 16 bits", width: MaxDimension + 1, height: 100},
		{name: "height above 16 bits", width: 100, height: MaxDimension + 1},
		{name: "int overflow width", width: math.MaxInt, height: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSize(tc.width, tc.height); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ValidateSize(%d, %d) = %v, want ErrInvalidArgument", tc.width, tc.height, err)
			}
		})
	}
}

func TestValidateSizeAcceptsRepresentableDimensions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{name: "minimum", width: 1, height: 1},
		{name: "typical", width: 1280, height: 720},
		{name: "maximum", width: MaxDimension, height: MaxDimension},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateSize(tc.width, tc.height); err != nil {
				t.Fatalf("ValidateSize(%d, %d) = %v, want nil", tc.width, tc.height, err)
			}
		})
	}
}

// A monitor can sit left of or above the primary one, so negative positions are
// ordinary and must be accepted.
func TestValidatePositionAcceptsNegativeCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{name: "origin", x: 0, y: 0},
		{name: "left and above", x: -1920, y: -1080},
		{name: "large but representable", x: math.MaxInt32, y: math.MinInt32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePosition(tc.x, tc.y); err != nil {
				t.Fatalf("ValidatePosition(%d, %d) = %v, want nil", tc.x, tc.y, err)
			}
		})
	}
}

func TestValidatePositionRejectsOutOfRangeCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{name: "x above", x: math.MaxInt32 + 1, y: 0},
		{name: "x below", x: math.MinInt32 - 1, y: 0},
		{name: "y above", x: 0, y: math.MaxInt32 + 1},
		{name: "y below", x: 0, y: math.MinInt32 - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePosition(tc.x, tc.y); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("ValidatePosition(%d, %d) = %v, want ErrInvalidArgument", tc.x, tc.y, err)
			}
		})
	}
}
