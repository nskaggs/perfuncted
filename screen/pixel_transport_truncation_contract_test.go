package screen

import (
	"image"
	"image/color"
	"testing"
)

func TestBGRAIncompleteTransportNeverReusesPreviousPixelData(t *testing.T) {
	// The compositor can terminate mid-frame. Decoding must only expose bytes
	// present in the current capture and must not leak bytes from prior frames.
	const width, height, stride = 3, 2, 16
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 0, 0, 0, 0, 13, 14, 15, 16}
	for _, tc := range []struct {
		name       string
		decode     func() *image.RGBA
		bounds     image.Rectangle
		filled     image.Point
		incomplete image.Point
	}{
		{"full padded frame", func() *image.RGBA { return decodeBGRA(data, width, height, stride) }, image.Rect(0, 0, 3, 2), image.Pt(0, 1), image.Pt(1, 1)},
		{"cropped padded frame", func() *image.RGBA { return decodeBGRARect(data, width, height, stride, image.Rect(0, 1, 3, 2)) }, image.Rect(0, 1, 3, 2), image.Pt(0, 1), image.Pt(1, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := tc.decode()
			if img.Bounds() != tc.bounds {
				t.Fatalf("unexpected image extent %v want %v", img.Bounds(), tc.bounds)
			}
			got := img.RGBAAt(tc.filled.X, tc.filled.Y)
			if got != (color.RGBA{R: 15, G: 14, B: 13, A: 255}) {
				t.Fatalf("available pixel: %v", got)
			}
			if got := img.RGBAAt(tc.incomplete.X, tc.incomplete.Y); got != (color.RGBA{}) {
				t.Fatalf("incomplete pixel contains stale content: %v", got)
			}
		})
	}
}

func TestCropRGBADoesNotReadMalformedBackingStorage(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  *image.RGBA
	}{
		{"missing backing pixels", &image.RGBA{Pix: nil, Stride: 8, Rect: image.Rect(0, 0, 2, 2)}},
		{"truncated backing pixels", &image.RGBA{Pix: []byte{1, 2, 3, 4}, Stride: 8, Rect: image.Rect(0, 0, 2, 2)}},
		{"zero stride", &image.RGBA{Pix: make([]byte, 16), Stride: 0, Rect: image.Rect(0, 0, 2, 2)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			crop := cropRGBA(tc.src, image.Rect(0, 0, 2, 2))
			if !crop.Bounds().Empty() {
				t.Fatalf("malformed RGBA storage was returned as an image: %v", crop.Bounds())
			}
		})
	}
}
