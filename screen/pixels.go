package screen

import (
	"image"
	"image/draw"
)

// decodeBGRA decodes raw BGRA pixel data (little-endian byte order) into an
// RGBA image. The stride parameter specifies bytes per row—this may be w*4 for
// tightly-packed data, or a larger compositor-supplied value with padding.
//
// This function is used by multiple backends (wlrscreencopy, extcapture, x11)
// that all receive BGRA frames from the compositor or X server.
func decodeBGRA(data []byte, w, h, stride int) *image.RGBA {
	if len(data) == 0 || w <= 0 || h <= 0 || stride <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rowBytes := w * 4
	for row := 0; row < h; row++ {
		srcStart := row * stride
		if srcStart >= len(data) {
			break
		}
		srcEnd := srcStart + rowBytes
		if srcEnd > len(data) {
			srcEnd = len(data)
		}
		dstOff := row * img.Stride
		copyBGRA(img.Pix[dstOff:dstOff+rowBytes], data[srcStart:srcEnd])
	}
	return img
}

// decodeBGRARect decodes a sub-rectangle of raw BGRA pixel data into an RGBA
// image. This avoids allocating and decoding the entire screen when only a
// small region is needed.
func decodeBGRARect(data []byte, w, h, stride int, rect image.Rectangle) *image.RGBA {
	if len(data) == 0 || w <= 0 || h <= 0 || stride <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	r := rect.Intersect(image.Rect(0, 0, w, h))
	if r.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	out := image.NewRGBA(r)
	rowBytes := r.Dx() * 4
	for y := 0; y < r.Dy(); y++ {
		srcStart := (r.Min.Y+y)*stride + r.Min.X*4
		if srcStart >= len(data) {
			break
		}
		srcEnd := srcStart + rowBytes
		if srcEnd > len(data) {
			srcEnd = len(data)
		}
		dstOff := y * out.Stride
		copyBGRA(out.Pix[dstOff:dstOff+rowBytes], data[srcStart:srcEnd])
	}
	return out
}

func copyBGRA(dst, src []byte) {
	n := len(src)
	if len(dst) < n {
		n = len(dst)
	}
	n -= n % 4
	for i := 0; i < n; i += 4 {
		dst[i+0] = src[i+2] // R ← B
		dst[i+1] = src[i+1] // G ← G
		dst[i+2] = src[i+0] // B ← R
		dst[i+3] = 0xff     // A
	}
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

func cropImage(img image.Image, rect image.Rectangle) image.Image {
	if rect.Empty() {
		return img
	}
	if rgba, ok := img.(*image.RGBA); ok {
		return cropRGBA(rgba, rect)
	}
	if si, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return si.SubImage(rect.Intersect(img.Bounds()))
	}
	r := rect.Intersect(img.Bounds())
	if r.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	out := image.NewRGBA(r)
	draw.Draw(out, out.Bounds(), img, r.Min, draw.Src)
	return out
}
