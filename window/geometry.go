package window

import (
	"fmt"
	"math"
)

// MaxDimension is the largest window edge every supported backend can
// represent. X11 window geometry is a 16-bit value, so a larger request cannot
// survive the protocol: it is truncated rather than rejected, which turns a bad
// argument into a silently wrong window size.
const MaxDimension = 1<<16 - 1

// ValidateSize reports whether width and height can be applied to a window.
//
// A window edge must be positive and no larger than MaxDimension. Zero has no
// meaning for a window and non-positive values are how an out-of-range request
// becomes a nonsense size, so reject them here rather than at each compositor.
func ValidateSize(width, height int) error {
	if err := validateDimension("width", width); err != nil {
		return err
	}
	return validateDimension("height", height)
}

func validateDimension(name string, value int) error {
	if value <= 0 {
		return fmt.Errorf("%w: window %s must be positive, got %d", ErrInvalidArgument, name, value)
	}
	if value > MaxDimension {
		return fmt.Errorf("%w: window %s %d exceeds the maximum of %d", ErrInvalidArgument, name, value, MaxDimension)
	}
	return nil
}

// ValidatePosition reports whether x and y can be applied to a window.
//
// Positions may be negative because a monitor can sit left of or above the
// primary one. The only constraint is that every backend can carry the value:
// the GNOME bridge speaks int32, which is the narrowest of the transports.
func ValidatePosition(x, y int) error {
	if err := validateCoordinate("x", x); err != nil {
		return err
	}
	return validateCoordinate("y", y)
}

func validateCoordinate(name string, value int) error {
	if value < math.MinInt32 || value > math.MaxInt32 {
		return fmt.Errorf("%w: window %s %d is outside the representable range of [%d, %d]",
			ErrInvalidArgument, name, value, int32(math.MinInt32), int32(math.MaxInt32))
	}
	return nil
}
