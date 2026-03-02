package screen

import (
	"image"
	"image/color"
)

// decodeBGRA decodes raw BGRA pixel data (little-endian byte order) into an
// RGBA image. The stride parameter specifies bytes per row—this may be w*4 for
// tightly-packed data, or a larger compositor-supplied value with padding.
//
// This function is used by multiple backends (wlrscreencopy, extcapture, x11)
// that all receive BGRA frames from the compositor or X server.
func decodeBGRA(data []byte, w, h, stride int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			off := row*stride + col*4
			if off+3 >= len(data) {
				return img
			}
			img.SetRGBA(col, row, color.RGBA{
				R: data[off+2],
				G: data[off+1],
				B: data[off],
				A: 0xff,
			})
		}
	}
	return img
}
