package screen

import (
	"image"
	"math/rand"
	"testing"
)

// A region read and the same region of a full compositor frame must produce
// identical pixels, including with row padding and clipped regions. This
// protects screenshot backends that use the fast region-only decode path.
func TestBGRARegionDecodeAgreesWithFullFrameAcrossPaddedLayouts(t *testing.T) {
	rng := rand.New(rand.NewSource(34918))
	for iteration := 0; iteration < 150; iteration++ {
		width := 1 + rng.Intn(18)
		height := 1 + rng.Intn(14)
		stride := 4*width + 4*rng.Intn(5)
		frame := make([]byte, height*stride)
		if _, err := rng.Read(frame); err != nil {
			t.Fatal(err)
		}
		full := decodeBGRA(frame, width, height, stride)
		rect := image.Rect(rng.Intn(width+9)-4, rng.Intn(height+9)-4, rng.Intn(width+9)-4, rng.Intn(height+9)-4)
		// Include arbitrary corners rather than requiring the requested region
		// to fall entirely within the compositor's output.
		expectedRect := rect.Intersect(full.Bounds())
		if expectedRect.Empty() {
			expectedRect = image.Rectangle{}
		}
		region := decodeBGRARect(frame, width, height, stride, rect)
		if region.Bounds() != expectedRect {
			t.Fatalf("case %d: region bounds %v, want %v", iteration, region.Bounds(), expectedRect)
		}
		for y := expectedRect.Min.Y; y < expectedRect.Max.Y; y++ {
			for x := expectedRect.Min.X; x < expectedRect.Max.X; x++ {
				if got, want := region.RGBAAt(x, y), full.RGBAAt(x, y); got != want {
					t.Fatalf("case %d at (%d,%d): region=%v full=%v", iteration, x, y, got, want)
				}
				if region.RGBAAt(x, y).A != 255 {
					t.Fatalf("case %d: decoder did not canonicalize alpha", iteration)
				}
			}
		}
	}
}
