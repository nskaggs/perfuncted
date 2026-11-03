package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// The logical and physical dimensions of one output must describe the same
// scale on both axes before replacing the previously known scale metadata.
func TestWaylandLogicalScaleRejectsInconsistentAxes(t *testing.T) {
	out := newWaylandOutput(wl.GlobalEvent{Name: 21, Interface: "wl_output", Version: 4})
	out.hasLogicalSize = true
	out.logicalW, out.logicalH = 1280, 720
	out.info.ResolutionW, out.info.ResolutionH = 1920, 1080
	out.updateLogicalScaleLocked()
	if out.info.ScaleNumerator != 3 || out.info.ScaleDenominator != 2 || out.info.Scale != 0 {
		t.Fatalf("expected fractional 3/2 initial scale, got %+v", out.info)
	}
	// A compositor may deliver a new width before the matching height.
	out.logicalW, out.logicalH = 1000, 720
	out.updateLogicalScaleLocked()
	if out.info.ScaleNumerator != 3 || out.info.ScaleDenominator != 2 || out.info.Scale != 0 {
		t.Fatalf("inconsistent axes overwrote last consistent scale: %+v", out.info)
	}
	out.logicalW, out.logicalH = 960, 540
	out.updateLogicalScaleLocked()
	if out.info.Scale != 2 || out.info.ScaleNumerator != 2 || out.info.ScaleDenominator != 1 {
		t.Fatalf("consistent integral scale was not adopted: %+v", out.info)
	}
}

func TestWaylandScaleArithmeticRejectsNonpositiveInputs(t *testing.T) {
	for _, tc := range []struct{ width, divisor int }{
		{-1, 2}, {0, 2}, {2, 0}, {2, -1},
	} {
		if got := ceilDiv(tc.width, tc.divisor); got != 0 {
			t.Fatalf("ceilDiv(%d,%d)=%d", tc.width, tc.divisor, got)
		}
		a, b := reducedRatio(tc.width, tc.divisor)
		if a != 0 || b != 0 {
			t.Fatalf("reducedRatio(%d,%d)=(%d,%d)", tc.width, tc.divisor, a, b)
		}
	}
	if got := ceilDiv(1921, 2); got != 961 {
		t.Fatalf("ceilDiv(1921,2)=%d", got)
	}
	if a, b := reducedRatio(3840, 2560); a != 3 || b != 2 {
		t.Fatalf("reducedRatio(3840,2560)=(%d,%d)", a, b)
	}
}
