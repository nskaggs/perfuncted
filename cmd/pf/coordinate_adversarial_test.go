package main

import (
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
	"math"
	"math/rand"
	"testing"
)

func TestWindowRelativePointFuzzTable(t *testing.T) {
	rng := rand.New(rand.NewSource(7319))
	spaces := []input.CoordinateSpaceInfo{{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, {Kind: input.CoordinateSpacePhysical, ScaleX: 1.25, ScaleY: 1.5}, {Kind: input.CoordinateSpacePhysical, ScaleX: 0.5, ScaleY: 0.8}, {Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 2}}
	for i := 0; i < 3000; i++ {
		w := window.Info{X: rng.Intn(3001) - 1500, Y: rng.Intn(3001) - 1500, W: rng.Intn(1800) + 1, H: rng.Intn(1200) + 1}
		space := spaces[i%len(spaces)]
		x := rng.Intn(w.W+4) - 2
		y := rng.Intn(w.H+4) - 2
		ax, ay, err := windowRelativePoint(w, x, y, space)
		valid := x >= 0 && y >= 0 && x < w.W && y < w.H
		if valid && err != nil || !valid && err == nil {
			t.Fatalf("case %d: geom=%+v x,y=%d,%d space=%+v: returned %d,%d err %v", i, w, x, y, space, ax, ay, err)
		}
		if valid {
			wantX := int(math.Round(float64(w.X+x) * space.ScaleX))
			wantY := int(math.Round(float64(w.Y+y) * space.ScaleY))
			if ax != wantX || ay != wantY {
				t.Fatalf("case %d: want %d,%d got %d,%d", i, wantX, wantY, ax, ay)
			}
		}
	}
}

func TestWindowRelativePointRejectsOverflowAndNonFinite(t *testing.T) {
	info := window.Info{X: math.MaxInt - 5, Y: math.MaxInt - 5, W: 100, H: 100}
	for _, space := range []input.CoordinateSpaceInfo{
		{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1},
		{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 2},
		{Kind: input.CoordinateSpacePhysical, ScaleX: math.MaxFloat64, ScaleY: 1},
		{Kind: input.CoordinateSpacePhysical, ScaleX: math.Inf(1), ScaleY: 1},
		{Kind: input.CoordinateSpacePhysical, ScaleX: math.NaN(), ScaleY: 1},
	} {
		if _, _, err := windowRelativePoint(info, 30, 30, space); err == nil {
			t.Errorf("accepted overflow/non-finite coordinate space %+v", space)
		}
	}
}

func TestScreenshotPointBoundsAndConversionsFuzzTable(t *testing.T) {
	rng := rand.New(rand.NewSource(2032))
	for i := 0; i < 2500; i++ {
		physicalW := rng.Intn(3840) + 1
		physicalH := rng.Intn(2160) + 1
		logicalW := rng.Intn(3840) + 1
		logicalH := rng.Intn(2160) + 1
		offsetX := rng.Intn(1200) - 600
		offsetY := rng.Intn(1200) - 600
		outputInfo := []output.Info{{Available: true, Geometry: output.Geometry{X: offsetX, Y: offsetY, W: logicalW, H: logicalH}, ResolutionW: physicalW, ResolutionH: physicalH}}
		x := rng.Intn(physicalW+4) - 2
		y := rng.Intn(physicalH+4) - 2
		rx, ry, err := screenshotPointToLogical(outputInfo, x, y)
		valid := x >= 0 && y >= 0 && x < physicalW && y < physicalH
		if valid && err != nil || !valid && err == nil {
			t.Fatalf("case %d: xy=%d,%d physical=%dx%d logical=%dx%d error=%v", i, x, y, physicalW, physicalH, logicalW, logicalH, err)
		}
		if valid {
			expectedX := offsetX + min(logicalW-1, int(math.Round(float64(x)*float64(logicalW)/float64(physicalW))))
			expectedY := offsetY + min(logicalH-1, int(math.Round(float64(y)*float64(logicalH)/float64(physicalH))))
			if expectedX != rx || expectedY != ry {
				t.Fatalf("case %d want %d,%d got %d,%d", i, expectedX, expectedY, rx, ry)
			}
		}
	}
}

func TestScreenshotPointRejectsLogicalOverflow(t *testing.T) {
	o := []output.Info{{Available: true, Geometry: output.Geometry{X: math.MaxInt - 5, Y: math.MaxInt - 5, W: 100, H: 100}, ResolutionW: 100, ResolutionH: 100}}
	if _, _, err := screenshotPointToLogical(o, 90, 90); err == nil {
		t.Fatal("accepted overflowing logical screen coordinate")
	}
}

func FuzzWindowRelativePoint(f *testing.F) {
	for _, seed := range [][4]int{{10, 10, 100, 100}, {-1, 5, 20, 30}, {0, 0, 1, 1}, {math.MaxInt, math.MaxInt, 200, 200}} {
		f.Add(seed[0], seed[1], seed[2], seed[3])
	}
	f.Fuzz(func(t *testing.T, x, y, w, h int) {
		if w < 1 || w > 100000 || h < 1 || h > 100000 {
			return
		}
		px, py, err := windowRelativePoint(window.Info{X: 100, Y: 200, W: w, H: h}, x, y, input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1})
		valid := x >= 0 && x < w && y >= 0 && y < h
		if valid && (err != nil || px != x+100 || py != y+200) || !valid && err == nil {
			t.Fatalf("(%d,%d) dims %dx%d => (%d,%d) err %v", x, y, w, h, px, py, err)
		}
	})
}

func TestScreenshotPixelNearFarEdgeNeverMapsOutsideLogicalOutput(t *testing.T) {
	for _, tc := range []struct{ physical, logical int }{{10000, 1}, {1000, 2}, {3840, 1}, {1920, 800}, {800, 1920}, {1, 3840}} {
		o := []output.Info{{Available: true, Geometry: output.Geometry{W: tc.logical, H: tc.logical}, ResolutionW: tc.physical, ResolutionH: tc.physical}}
		for _, px := range []int{0, tc.physical / 2, tc.physical - 1} {
			x, y, err := screenshotPointToLogical(o, px, px)
			if err != nil {
				t.Fatalf("scale=%v pixel=%d err=%v", tc, px, err)
			}
			if x < 0 || y < 0 || x >= tc.logical || y >= tc.logical {
				t.Fatalf("scale=%v pixel=%d -> out of bounds (%d,%d)", tc, px, x, y)
			}
		}
	}
}
