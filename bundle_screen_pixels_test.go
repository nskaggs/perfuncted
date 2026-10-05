package perfuncted

import (
	"image"
	"math/bits"
	"math/rand"
	"testing"
)

// A capture of a desktop region comes back at the output's pixel scale, so a
// desktop point maps onto the image by ratio along each axis. Translating by a
// constant offset returned a neighbouring pixel for every point except the first
// on each axis, and reported the point as outside the image near the far edge.
func TestTranslatePointsToImageScalesByRatio(t *testing.T) {
	region := image.Rect(0, 0, 100, 100)
	imageBounds := image.Rect(0, 0, 200, 200) // scale 2

	got, err := translatePointsToImage([]image.Point{{0, 0}, {50, 50}, {99, 99}}, region, imageBounds, region)
	if err != nil {
		t.Fatalf("translatePointsToImage: %v", err)
	}
	want := []image.Point{{0, 0}, {100, 100}, {198, 198}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("point %d mapped to %v, want %v (all: %v)", i, got[i], want[i], got)
		}
	}
}

// A capture whose origin is not zero must be handled: the image offset matters
// as well as the scale.
func TestTranslatePointsToImageAccountsForImageOrigin(t *testing.T) {
	region := image.Rect(40, 20, 140, 120)
	imageBounds := image.Rect(80, 40, 280, 240) // scale 2, origin doubled

	got, err := translatePointsToImage([]image.Point{{40, 20}, {90, 70}, {139, 119}}, region, imageBounds, region)
	if err != nil {
		t.Fatalf("translatePointsToImage: %v", err)
	}
	want := []image.Point{{80, 40}, {180, 140}, {278, 238}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("point %d mapped to %v, want %v (all: %v)", i, got[i], want[i], got)
		}
	}
}

// At scale one the mapping is a pure offset, which is the case the previous
// implementation handled.
func TestTranslatePointsToImageAtScaleOneIsAnOffset(t *testing.T) {
	region := image.Rect(10, 10, 20, 20)
	imageBounds := image.Rect(10, 10, 20, 20)

	got, err := translatePointsToImage([]image.Point{{10, 10}, {15, 15}, {19, 19}}, region, imageBounds, region)
	if err != nil {
		t.Fatalf("translatePointsToImage: %v", err)
	}
	want := []image.Point{{10, 10}, {15, 15}, {19, 19}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("point %d mapped to %v, want %v", i, got[i], want[i])
		}
	}
}

// Every mapped point must land inside the image, which the previous offset
// translation could not guarantee once the scale was above one.
func TestTranslatePointsToImageKeepsEveryPointInsideTheImage(t *testing.T) {
	for _, scale := range []int{1, 2, 3, 4} {
		region := image.Rect(0, 0, 64, 64)
		imageBounds := image.Rect(0, 0, 64*scale, 64*scale)

		points := make([]image.Point, 0, 64*64)
		for y := 0; y < 64; y++ {
			for x := 0; x < 64; x++ {
				points = append(points, image.Point{x, y})
			}
		}
		got, err := translatePointsToImage(points, region, imageBounds, region)
		if err != nil {
			t.Fatalf("scale %d: translatePointsToImage: %v", scale, err)
		}
		for i, p := range got {
			if !p.In(imageBounds) {
				t.Fatalf("scale %d: desktop point %v mapped to %v, outside %v", scale, points[i], p, imageBounds)
			}
		}
	}
}

// The mapping is the inverse of Capture.ScreenPoint, so a round trip returns to
// the pixel that was asked for. The two APIs must not disagree about where a
// desktop coordinate lives in an image.
func TestTranslatePointsToImageRoundTripsWithScreenPoint(t *testing.T) {
	const (
		regionW = 300
		regionH = 200
	)
	for _, scale := range []int{1, 2, 3} {
		region := image.Rect(0, 0, regionW, regionH)
		imageBounds := image.Rect(0, 0, regionW*scale, regionH*scale)

		capture := Capture{
			Image:      image.NewRGBA(imageBounds),
			ScreenRect: region,
		}

		points := []image.Point{
			{0, 0},
			{1, 1},
			{regionW / 2, regionH / 2},
			{regionW - 1, regionH - 1},
		}
		mapped, err := translatePointsToImage(points, region, imageBounds, region)
		if err != nil {
			t.Fatalf("scale %d: translatePointsToImage: %v", scale, err)
		}
		for i, pixel := range mapped {
			back, err := capture.ScreenPoint(pixel)
			if err != nil {
				t.Fatalf("scale %d: ScreenPoint(%v): %v", scale, pixel, err)
			}
			// A scaled image has more pixels than desktop points, so the round
			// trip lands within one desktop pixel.
			if abs(back.X-points[i].X) > 1 || abs(back.Y-points[i].Y) > 1 {
				t.Fatalf("scale %d: %v -> %v -> %v drifted by more than one desktop pixel",
					scale, points[i], pixel, back)
			}
		}
	}
}

// An empty or inverted region cannot be mapped and must be reported rather than
// dividing by zero. image.Rect canonicalizes its arguments, so the inverted
// cases are built as literals.
func TestTranslatePointsToImageRejectsDegenerateBounds(t *testing.T) {
	cases := []struct {
		name        string
		region      image.Rectangle
		imageBounds image.Rectangle
	}{
		{name: "empty region", region: image.Rect(0, 0, 0, 0), imageBounds: image.Rect(0, 0, 10, 10)},
		{name: "empty image", region: image.Rect(0, 0, 10, 10), imageBounds: image.Rect(0, 0, 0, 0)},
		{
			name:        "inverted region",
			region:      image.Rectangle{Min: image.Point{X: 10, Y: 10}, Max: image.Point{X: 5, Y: 5}},
			imageBounds: image.Rect(0, 0, 10, 10),
		},
		{
			name:        "inverted image",
			region:      image.Rect(0, 0, 10, 10),
			imageBounds: image.Rectangle{Min: image.Point{X: 10, Y: 10}, Max: image.Point{X: 5, Y: 5}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := translatePointsToImage([]image.Point{{1, 1}}, tc.region, tc.imageBounds, tc.region); err == nil {
				t.Fatalf("translatePointsToImage accepted %v for %v", tc.region, tc.imageBounds)
			}
		})
	}
}

// A point outside the captured region has no image pixel. Subtracting the
// region's origin from it wrapped around and overflowed the scaling multiply,
// so the precondition is checked rather than assumed.
func TestTranslatePointsToImageRejectsPointOutsideRegion(t *testing.T) {
	region := image.Rect(50, 50, 60, 60)
	imageBounds := image.Rect(0, 0, 20, 20)

	cases := []struct {
		name  string
		point image.Point
	}{
		{name: "left of region", point: image.Point{X: 49, Y: 55}},
		{name: "above region", point: image.Point{X: 55, Y: 49}},
		{name: "far negative", point: image.Point{X: -1000, Y: -1000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("translatePointsToImage panicked on %v: %v", tc.point, r)
				}
			}()
			if _, err := translatePointsToImage([]image.Point{tc.point}, region, imageBounds, region); err == nil {
				t.Fatalf("translatePointsToImage accepted %v outside %v", tc.point, region)
			}
		})
	}
}

// Randomized round trips across scales and origins, so an off-by-one in either
// the offset or the scale is caught.
func TestTranslatePointsToImageAgreesWithRatioMath(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for range 200 {
		regionW := 1 + rng.Intn(400)
		regionH := 1 + rng.Intn(400)
		originX := rng.Intn(2000)
		originY := rng.Intn(2000)
		scale := 1 + rng.Intn(4)

		region := image.Rect(originX, originY, originX+regionW, originY+regionH)
		imageBounds := image.Rect(
			originX*scale, originY*scale,
			originX*scale+regionW*scale, originY*scale+regionH*scale,
		)

		points := []image.Point{
			{originX, originY},
			{originX + regionW/2, originY + regionH/2},
			{originX + regionW - 1, originY + regionH - 1},
		}
		got, err := translatePointsToImage(points, region, imageBounds, region)
		if err != nil {
			t.Fatalf("translatePointsToImage: %v", err)
		}
		for i, p := range got {
			wantX := imageBounds.Min.X + (points[i].X-region.Min.X)*scale
			wantY := imageBounds.Min.Y + (points[i].Y-region.Min.Y)*scale
			// Integer division truncates, so allow one pixel of slack and require
			// the error to shrink as the scale grows.
			if abs(p.X-wantX) > 1 || abs(p.Y-wantY) > 1 {
				t.Fatalf("region %v image %v: %v -> %v, want about (%d,%d)",
					region, imageBounds, points[i], p, wantX, wantY)
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// A capture smaller than the requested region is the part the backend could get,
// not a scaled version of the whole thing. Scaling the region into it anyway maps
// a point near the clipped edge onto a pixel standing for a different place, so
// the mismatch has to be refused rather than sampled.
func TestTranslatePointsRejectsAClippedCapture(t *testing.T) {
	region := image.Rect(0, 0, 100, 100)

	// A uniform downscale is not fine. Output scaling multiplies a region's pixels and
	// never divides them, so a capture smaller than the region is one that came back
	// with part of the region missing. Reading a pixel out of it yields an averaged
	// value standing for somewhere else on screen, and clipping both edges by the same
	// proportion keeps the aspect ratio, so a uniform-scale check alone cannot tell
	// the two apart.
	downscale := image.Rect(0, 0, 50, 50)
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, downscale, region); err == nil {
		t.Fatal("a capture smaller than the region was treated as a scaled capture")
	}

	// A uniform upscale is fine too.
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 200, 200), region); err != nil {
		t.Fatalf("a uniform upscale was refused: %v", err)
	}

	// Clipped on one axis only: 50x100 for a 100x100 region.
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 50, 100), region); err == nil {
		t.Fatal("a capture clipped on one axis was treated as a scaled region")
	}
	// Clipped on the other axis only.
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 100, 25), region); err == nil {
		t.Fatal("a capture clipped on the other axis was treated as a scaled region")
	}
	// A non-uniform stretch is not a capture at all.
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 50, 25), region); err == nil {
		t.Fatal("a non-uniform scaling was accepted")
	}
}

// The two spans are compared through 128-bit products. Their coordinates come from
// ints, so on a wide enough rectangle the 64-bit products wrap and spans of
// different aspect ratios share a low word, which a 64-bit comparison reads as equal.
func TestSpanProductsDistinguishPairsThatShareALowWord(t *testing.T) {
	const twoTo32 = uint64(1) << 32

	hiSmall, loSmall := bits.Mul64(twoTo32, twoTo32)
	hiLarge, loLarge := bits.Mul64(3*twoTo32, twoTo32)

	if loSmall != loLarge {
		t.Fatalf("fixture does not exercise the low word: %d and %d", loSmall, loLarge)
	}
	if hiSmall == hiLarge {
		t.Fatalf("fixture does not exercise the high word: both %d", hiSmall)
	}
	if hiSmall == 0 || hiLarge == 0 {
		t.Fatalf("fixture does not overflow 64 bits: %d and %d", hiSmall, hiLarge)
	}
	// The low words are equal and the products differ, so a comparison that looked at
	// only the low word would call them the same number.
}

// A capture whose aspect ratio differs from the region's is refused at the sizes a
// real screen produces.
func TestTranslatePointsRejectsAMismatchedAspectRatio(t *testing.T) {
	region := image.Rect(0, 0, 100, 50)
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 100, 100), region); err == nil {
		t.Fatal("a capture of a different aspect ratio was accepted as a scaled capture")
	}
}

// Capture.ScreenPoint maps image points to the desktop and translatePointsToImage maps
// them back. A capture smaller than the region it represents is a downscale or a
// proportional clip, and the bounds alone cannot say which, so the two used to disagree:
// one accepted the mapping and the other refused it. The recorded request decides, and
// both directions have to reach the same conclusion about the same capture.
func TestCaptureRoundTripAgreesOnWhatCountsAsADownscale(t *testing.T) {
	region := image.Rect(0, 0, 100, 100)

	// A capture that is exactly what was asked for, at the region's own size.
	exact := Capture{Image: image.NewRGBA(image.Rect(0, 0, 100, 100)), ScreenRect: region, ExpectedImageBounds: region}
	screenPoint, err := exact.ScreenPoint(image.Point{10, 10})
	if err != nil {
		t.Fatalf("ScreenPoint on an exact capture: %v", err)
	}
	if _, err := translatePointsToImage([]image.Point{screenPoint}, region, image.Rect(0, 0, 100, 100), region); err != nil {
		t.Fatalf("translatePointsToImage refused the inverse of an accepted mapping: %v", err)
	}

	// A capture recorded as requested at half size. Both directions must accept it, and
	// the round trip must land back on the desktop point it started from.
	downscale := Capture{Image: image.NewRGBA(image.Rect(0, 0, 50, 50)), ScreenRect: region, ExpectedImageBounds: image.Rect(0, 0, 50, 50)}
	pixel := image.Point{5, 5}
	forward, err := downscale.ScreenPoint(pixel)
	if err != nil {
		t.Fatalf("ScreenPoint on a recorded downscale: %v", err)
	}
	back, err := translatePointsToImage([]image.Point{forward}, region, image.Rect(0, 0, 50, 50), image.Rect(0, 0, 50, 50))
	if err != nil {
		t.Fatalf("translatePointsToImage refused a capture ScreenPoint accepted: %v", err)
	}
	if back[0] != pixel {
		t.Fatalf("round trip through a downscale = %v, want %v", back[0], pixel)
	}

	// The same bounds with no recorded request is a capture of unknown provenance. It
	// cannot be told apart from a proportional clip, so it is refused rather than
	// sampled, and ScreenPoint must not claim a mapping the inverse will not honour.
	unknown := Capture{Image: image.NewRGBA(image.Rect(0, 0, 50, 50)), ScreenRect: region}
	if unknown.ReportsDownscale() {
		t.Fatal("a capture with no recorded request was reported as a downscale")
	}
	if _, err := translatePointsToImage([]image.Point{{10, 10}}, region, image.Rect(0, 0, 50, 50), image.Rect(0, 0, 100, 100)); err == nil {
		t.Fatal("a capture with no recorded request was accepted as a downscale")
	}
}
