package screen

import (
	"image"
	"image/color"
	"testing"
)

// Frame dimensions and crop coordinates come from external compositors.
// Bad metadata must not cause unsafe allocation or turn incomplete data into
// apparently valid nonzero pixels.
func TestRawBGRAFrameRejectsOverflowingHeightBeforeAllocation(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if got := decodeBGRA([]byte{9}, 2, maxInt, 8); !got.Bounds().Empty() {
		t.Fatalf("overflow height accepted: %v", got.Bounds())
	}
}

func TestRawBGRARectRefusesInvalidAndUnreachableRegions(t *testing.T) {
	for _, tc := range []struct {
		name         string
		w, h, stride int
		rect         image.Rectangle
	}{
		{"empty bytes", 1, 1, 4, image.Rect(0, 0, 1, 1)},
		{"invalid height", 1, 0, 4, image.Rect(0, 0, 1, 1)},
		{"invalid stride", 1, 1, 0, image.Rect(0, 0, 1, 1)},
		{"outside output", 1, 1, 4, image.Rect(5, 5, 6, 6)},
		{"oversized width", int(^uint(0) >> 1), 1, 4, image.Rect(0, 0, 1, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte{1, 2, 3, 4}
			if tc.name == "empty bytes" {
				data = nil
			}
			if img := decodeBGRARect(data, tc.w, tc.h, tc.stride, tc.rect); !img.Bounds().Empty() {
				t.Fatalf("invalid frame produced pixels: %v", img.Bounds())
			}
		})
	}
}

func TestCropRGBARejectsMalformedStorageAndPreservesTransparentGaps(t *testing.T) {
	source := &image.RGBA{Pix: []byte{1, 2, 3, 4}, Stride: 0, Rect: image.Rect(0, 0, 2, 2)}
	cropped := cropRGBA(source, image.Rect(0, 0, 1, 1))
	if got := cropped.RGBAAt(0, 0); got != (color.RGBA{}) {
		t.Fatalf("zero-stride frame disclosed pixels: %+v", got)
	}
	source.Stride = 8
	source.Rect = image.Rect(0, 0, 2, 2)
	cropped = cropRGBA(source, image.Rect(-1, -1, 3, 3))
	if cropped.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("crop should preserve requested geometry: %v", cropped.Bounds())
	}
	for _, p := range []image.Point{{0, 0}, {1, 1}, {2, 2}, {3, 3}} {
		if got := cropped.RGBAAt(p.X, p.Y); got != (color.RGBA{}) {
			t.Fatalf("invalid backing pixels became visible at %v: %v", p, got)
		}
	}
}

func TestCropRGBARejectsMalformedSourceRectangleDimensions(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	source := &image.RGBA{Pix: []byte{1, 2, 3, 4}, Stride: 4, Rect: image.Rectangle{Min: image.Pt(-maxInt, 0), Max: image.Pt(maxInt, 1)}}
	got := cropRGBA(source, image.Rect(0, 0, 1, 1))
	if c := got.RGBAAt(0, 0); c != (color.RGBA{}) {
		t.Fatalf("overflowing source exposed pixels: %v", c)
	}
}

func TestCropImageOutsideRegionNeverReadsUnderlyingImage(t *testing.T) {
	source := solidTestImage{rect: image.Rect(0, 0, 1, 1), c: color.RGBA{R: 7, A: 255}}
	got := cropImage(source, image.Rect(10, 10, 12, 12))
	if !got.Bounds().Empty() {
		t.Fatalf("out-of-range non-subimage crop reported data: %v", got.Bounds())
	}
}
