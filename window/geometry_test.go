package window

import (
	"context"
	"errors"
	"math"
	"strings"
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

func TestSignedNumericIDRejectsOverflow(t *testing.T) {
	if _, err := signedNumericID("9223372036854775808"); err == nil {
		t.Fatal("signedNumericID accepted a value above MaxInt64")
	}
}

func TestX11WindowIDRejectsOverflow(t *testing.T) {
	_, err := x11WindowID("4294967296")
	if err == nil || !strings.Contains(err.Error(), "X11 window id range") {
		t.Fatalf("x11WindowID error = %v, want X11 range error", err)
	}
}

func TestSwayIterateWindowsSkipsNegativeIDs(t *testing.T) {
	manager := &SwayManager{conn: newSuccessSwayConn([]byte(`{"id":1,"type":"root","nodes":[{"id":-1,"type":"con","name":"bad"},{"id":2,"type":"con","name":"good"}]}`))}
	var got []Info
	for info, err := range manager.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, info)
	}
	if len(got) != 1 || got[0].Title != "good" {
		t.Fatalf("IterateWindows = %#v, want only good window", got)
	}
}

// Native IDs are serialized as decimal text. Leading zeroes do not mean octal:
// accepting base-0 parser semantics would route a request for ID 10 to ID 8.
func TestNumericNativeIDsUseDecimalNotOctal(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want uint64
	}{
		{"0", 0}, {"08", 8}, {"010", 10}, {"00042", 42},
		{"0x2a", 42}, {"0X2A", 42},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := numericID(tc.raw)
			if err != nil || got != tc.want {
				t.Fatalf("numericID(%q) = %d, %v; want %d", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestX11NativeIDsDoNotRetargetLeadingZeroDecimal(t *testing.T) {
	got, err := x11WindowID("010")
	if err != nil || uint64(got) != 10 {
		t.Fatalf("X11 handle leading-zero decimal = %d, %v; want 10", got, err)
	}
}
