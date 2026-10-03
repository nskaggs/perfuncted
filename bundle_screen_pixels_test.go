package perfuncted

import (
	"image"
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

	got, err := translatePointsToImage([]image.Point{{0, 0}, {50, 50}, {99, 99}}, region, imageBounds)
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

	got, err := translatePointsToImage([]image.Point{{40, 20}, {90, 70}, {139, 119}}, region, imageBounds)
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

	got, err := translatePointsToImage([]image.Point{{10, 10}, {15, 15}, {19, 19}}, region, imageBounds)
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
		got, err := translatePointsToImage(points, region, imageBounds)
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
		mapped, err := translatePointsToImage(points, region, imageBounds)
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
			if _, err := translatePointsToImage([]image.Point{{1, 1}}, tc.region, tc.imageBounds); err == nil {
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
			if _, err := translatePointsToImage([]image.Point{tc.point}, region, imageBounds); err == nil {
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
		got, err := translatePointsToImage(points, region, imageBounds)
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

	// A uniform downscale is fine: 50x50 for a 100x100 region.
	uniform := image.Rect(0, 0, 50, 50)
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, uniform); err != nil {
		t.Fatalf("a uniform scaling was refused: %v", err)
	}

	// A uniform upscale is fine too.
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 200, 200)); err != nil {
		t.Fatalf("a uniform upscale was refused: %v", err)
	}

	// Clipped on one axis only: 50x100 for a 100x100 region.
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 50, 100)); err == nil {
		t.Fatal("a capture clipped on one axis was treated as a scaled region")
	}
	// Clipped on the other axis only.
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 100, 25)); err == nil {
		t.Fatal("a capture clipped on the other axis was treated as a scaled region")
	}
	// A non-uniform stretch is not a capture at all.
	if _, err := translatePointsToImage([]image.Point{{X: 10, Y: 10}}, region, image.Rect(0, 0, 50, 25)); err == nil {
		t.Fatal("a non-uniform scaling was accepted")
	}
}
