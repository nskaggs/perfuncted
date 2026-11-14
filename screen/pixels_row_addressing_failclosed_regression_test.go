package screen

import (
	"image"
	"image/color"
	"testing"
)

// Compositor pixel buffers are untrusted. A row with an impossible source
// address must not copy any bytes into the destination, even if both backing
// slices happen to contain data.
func TestRGBARowCopyRejectsInvalidSourceAddressWithoutChangingDestination(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name   string
		src    *image.RGBA
		region image.Rectangle
		y      int
	}{
		{"negative source row", &image.RGBA{Pix: make([]byte, 16), Stride: 8, Rect: image.Rect(0, 3, 2, 5)}, image.Rect(0, 0, 2, 1), 0},
		{"negative source column", &image.RGBA{Pix: make([]byte, 16), Stride: 8, Rect: image.Rect(3, 0, 5, 2)}, image.Rect(0, 0, 2, 1), 0},
		{"source row offset overflow", &image.RGBA{Pix: make([]byte, 16), Stride: 8, Rect: image.Rect(0, 0, 2, 2)}, image.Rect(0, 0, 2, 1), maxInt},
		{"source column byte offset overflow", &image.RGBA{Pix: make([]byte, 16), Stride: 8, Rect: image.Rect(0, 0, 2, 2)}, image.Rect(maxInt/4+1, 0, maxInt/4+3, 1), 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := image.NewRGBA(image.Rect(0, 0, 2, 2))
			dst.SetRGBA(0, 0, color.RGBA{R: 33, G: 44, B: 55, A: 255})
			before := append([]byte(nil), dst.Pix...)
			copyRGBARow(dst, tc.src, tc.region, 0, 0, tc.y, 8)
			for i := range before {
				if before[i] != dst.Pix[i] {
					t.Fatalf("invalid source addressed destination at offset %d", i)
				}
			}
		})
	}
}

func TestPixelRectDimensionsRejectsCoordinateSubtractionOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if _, _, ok := safePixelRectDimensions(image.Rectangle{Min: image.Pt(0, -maxInt), Max: image.Pt(1, maxInt)}); ok {
		t.Fatal("overflowing row coordinate span accepted")
	}
}
