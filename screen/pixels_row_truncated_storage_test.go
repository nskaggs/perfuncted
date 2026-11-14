package screen

import (
	"bytes"
	"image"
	"testing"
)

// A decoder must never partially copy a compositor row whose source or
// destination byte storage cannot hold the complete requested pixel span.
func TestRGBARowCopyRejectsTruncatedCompositorStorageWithoutPartialWrite(t *testing.T) {
	valid := image.NewRGBA(image.Rect(0, 0, 2, 1))
	for i := range valid.Pix {
		valid.Pix[i] = byte(i + 1)
	}
	tests := []struct {
		name        string
		destination *image.RGBA
		source      *image.RGBA
	}{
		{name: "source truncated", destination: image.NewRGBA(image.Rect(0, 0, 2, 1)), source: &image.RGBA{Pix: []byte{1, 2, 3, 4}, Stride: 8, Rect: image.Rect(0, 0, 2, 1)}},
		{name: "destination truncated", destination: &image.RGBA{Pix: []byte{9, 9, 9, 9}, Stride: 8, Rect: image.Rect(0, 0, 2, 1)}, source: valid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.destination.Pix {
				tc.destination.Pix[i] = 0xaa
			}
			before := bytes.Clone(tc.destination.Pix)
			copyRGBARow(tc.destination, tc.source, image.Rect(0, 0, 2, 1), 0, 0, 0, 8)
			if !bytes.Equal(tc.destination.Pix, before) {
				t.Fatalf("partial pixel bytes escaped truncated storage: got=%x want=%x", tc.destination.Pix, before)
			}
		})
	}
}
