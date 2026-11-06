package find

import (
	"hash/adler32"
	"image"
	"testing"
)

// A screenshotter can expose an RGBA image with valid rectangle/stride
// metadata whose pixel allocation was truncated. Neither the optimized CRC
// path nor a caller-supplied hasher may read that buffer or hash a prefix as
// though it were a complete frame.
func TestPixelHashRejectsTruncatedRGBARowsWithDefaultAndCustomHash(t *testing.T) {
	for _, tc := range []struct {
		name   string
		image  *image.RGBA
		region image.Rectangle
	}{
		{name: "truncated first scanline", image: &image.RGBA{Pix: make([]byte, 7), Stride: 8, Rect: image.Rect(10, 20, 12, 22)}, region: image.Rect(10, 20, 12, 22)},
		{name: "truncated second scanline", image: &image.RGBA{Pix: make([]byte, 12), Stride: 8, Rect: image.Rect(10, 20, 12, 22)}, region: image.Rect(10, 20, 12, 22)},
		{name: "truncated subimage final row", image: &image.RGBA{Pix: make([]byte, 27), Stride: 16, Rect: image.Rect(10, 20, 13, 22)}, region: image.Rect(11, 20, 13, 22)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, hasher := range []struct {
				name string
				new  Hasher
			}{{name: "crc32 default"}, {name: "custom adler32", new: adler32.New}} {
				t.Run(hasher.name, func(t *testing.T) {
					if got := pixelHashImage(tc.image, tc.region, hasher.new); got != 0 {
						t.Fatalf("truncated frame hashed as %08x", got)
					}
				})
			}
		})
	}
}
