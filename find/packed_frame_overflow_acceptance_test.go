package find

import (
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
)

func TestBGRAFrameValidationRejectsOverflowAndTruncationWithoutPanics(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		data                  []byte
		width, height, stride int
	}{
		{"empty", nil, 1, 1, 4},
		{"negative width", make([]byte, 4), -1, 1, 4},
		{"zero height", make([]byte, 4), 1, 0, 4},
		{"zero stride", make([]byte, 4), 1, 1, 0},
		{"width multiplied by four overflows", make([]byte, 4), math.MaxInt, 1, math.MaxInt},
		{"stride smaller than packed row", make([]byte, 8), 2, 1, 4},
		{"total rows overflow", make([]byte, 16), 1, math.MaxInt, math.MaxInt},
		{"truncated last row", make([]byte, 11), 1, 3, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := bgraFrameBounds(tc.data, tc.width, tc.height, tc.stride); ok || !got.Empty() {
				t.Fatalf("invalid layout accepted: %+v", got)
			}
			if hash, err := PixelHashBGRA(tc.data, tc.width, tc.height, tc.stride, image.Rectangle{}); hash != 0 || !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("invalid frame hash=%08x err=%v", hash, err)
			}
		})
	}
	data, _, stride := syntheticBGRAFrame(3, 2, 5, 19)
	for _, rect := range []image.Rectangle{image.Rect(4, 0, 5, 1), image.Rect(0, 3, 1, 4), image.Rect(-9, -3, -1, -1)} {
		if hash, err := PixelHashBGRA(data, 3, 2, stride, rect); hash != 0 || !errors.Is(err, ErrEmptyRegion) {
			t.Fatalf("off-screen %v hash=%08x err=%v", rect, hash, err)
		}
	}
}

func TestPackedImageLayoutAndPixelScanRejectMalformedBuffers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rect   image.Rectangle
		stride int
		bytes  int
	}{
		{"empty", image.Rectangle{}, 0, 0},
		{"negative stride", image.Rect(0, 0, 2, 2), -4, 16},
		{"short row", image.Rect(0, 0, 2, 2), 4, 16},
		{"row-count overflow", image.Rect(0, 0, 2, math.MaxInt), math.MaxInt, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if n, ok := packedBufferSize(tc.rect, tc.stride); ok || n != 0 {
				t.Fatalf("invalid packed layout size=%d ok=%v", n, ok)
			}
		})
	}
	malformed := &image.RGBA{Pix: make([]byte, 3), Stride: 8, Rect: image.Rect(20, 40, 22, 42)}
	if point, ok := PixelFound(malformed, image.Rectangle{}, color.RGBA{R: 255, A: 255}, 0); ok || point != (image.Point{}) {
		t.Fatalf("truncated RGBA returned %+v %v", point, ok)
	}
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if found := matchAt(malformed, ref, 20, 40); found {
		t.Fatal("truncated source matched full image")
	}
	// The source layout is valid, but the reference has a matching stride with
	// too few bytes for even one pixel. A reported match would be untrusted.
	source := image.NewRGBA(image.Rect(20, 40, 22, 42))
	shortReference := &image.RGBA{Pix: make([]byte, 2), Stride: 4, Rect: image.Rect(0, 0, 1, 1)}
	if found := matchAt(source, shortReference, 20, 40); found {
		t.Fatal("truncated reference matched valid image")
	}
	if _, err := LocateExactInImage(source, source.Bounds(), shortReference); err == nil {
		t.Fatal("truncated reference was accepted for exact image lookup")
	}
}

func TestPixelFoundZeroOriginCaptureRestrictsToRequestedExtent(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	target := color.RGBA{R: 230, G: 5, B: 10, A: 255}
	canvas.SetRGBA(3, 3, target)
	rect := image.Rect(50, 60, 52, 62)
	if p, ok := PixelFound(canvas, rect, target, 0); ok || p != (image.Point{}) {
		t.Fatalf("search escaped requested extent: p=%v found=%v", p, ok)
	}
	canvas.SetRGBA(1, 1, target)
	if p, ok := PixelFound(canvas, rect, target, 0); !ok || p != (image.Pt(51, 61)) {
		t.Fatalf("zero-origin translated p=%v found=%v", p, ok)
	}
}

func TestRectangleAreaRejectsIntegerOverflow(t *testing.T) {
	if area, ok := rectangleArea(image.Rectangle{}); area != 0 || ok {
		t.Fatalf("empty geometry area=%d ok=%v", area, ok)
	}
	if area, ok := rectangleArea(image.Rect(0, 0, math.MaxInt, math.MaxInt)); area != 0 || ok {
		t.Fatalf("overflow geometry area=%d ok=%v", area, ok)
	}
	if area, ok := rectangleArea(image.Rect(0, 0, 2, 3)); area != 6 || !ok {
		t.Fatalf("normal geometry area=%d ok=%v", area, ok)
	}
}
