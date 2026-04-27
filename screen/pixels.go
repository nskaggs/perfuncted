package screen

import (
	"image"
)

// decodeBGRA decodes raw BGRA pixel data (little-endian byte order) into an
// RGBA image. The stride parameter specifies bytes per row—this may be w*4 for
// tightly-packed data, or a larger compositor-supplied value with padding.
//
// This function is used by multiple backends (wlrscreencopy, extcapture, x11)
// that all receive BGRA frames from the compositor or X server.
func decodeBGRA(data []byte, w, h, stride int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rowBytes := w * 4
	for row := 0; row < h; row++ {
		srcRow := data[row*stride : row*stride+rowBytes]
		dstOff := row * img.Stride
		dst := img.Pix[dstOff : dstOff+rowBytes]
		// iterate by bytes to reduce multiplications inside loop
		for s := 0; s < rowBytes; s += 4 {
			dst[s+0] = srcRow[s+2] // R ← B
			dst[s+1] = srcRow[s+1] // G ← G
			dst[s+2] = srcRow[s+0] // B ← R
			dst[s+3] = 0xff        // A
		}
	}
	return img
}

// cropRGBA extracts the sub-rectangle rect from a full-screen RGBA image,
// returning a new image with bounds starting at (0, 0). Pixels outside the
// source image are left as zero (transparent black).
func cropRGBA(src *image.RGBA, rect image.Rectangle) *image.RGBA {
	if src.Bounds() == rect && rect.Min == (image.Point{0, 0}) {
		return src
	}
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	r := rect.Intersect(src.Bounds())
	if r.Empty() {
		return out
	}
	// dstX/dstY: top-left offset within out for the intersected region.
	dstX := r.Min.X - rect.Min.X
	dstY := r.Min.Y - rect.Min.Y
	w4 := r.Dx() * 4
	for y := 0; y < r.Dy(); y++ {
		srcOff := (r.Min.Y+y-src.Rect.Min.Y)*src.Stride + (r.Min.X-src.Rect.Min.X)*4
		dstOff := (dstY+y)*out.Stride + dstX*4
		copy(out.Pix[dstOff:dstOff+w4], src.Pix[srcOff:srcOff+w4])
	}
	return out
}
