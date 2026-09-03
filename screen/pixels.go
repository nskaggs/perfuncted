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
	rowBytes, ok := safePixelRowBytes(w)
	if !ok || stride < rowBytes || h > int(^uint(0)>>1)/rowBytes {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for row := 0; row < h; row++ {
		if row > int(^uint(0)>>1)/stride {
			break
		}
		srcStart := row * stride
		if srcStart >= len(data) {
			break
		}
		srcEnd := len(data)
		if rowBytes <= len(data)-srcStart {
			srcEnd = srcStart + rowBytes
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
	fullRowBytes, ok := safePixelRowBytes(w)
	if !ok || stride < fullRowBytes {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	r := rect.Intersect(image.Rect(0, 0, w, h))
	if r.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	rowBytes, ok := safePixelRowBytes(r.Dx())
	if !ok || r.Dy() > int(^uint(0)>>1)/rowBytes {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	out := image.NewRGBA(r)
	xOffset := r.Min.X * 4
	for y := 0; y < r.Dy(); y++ {
		row := r.Min.Y + y
		if row > (int(^uint(0)>>1)-xOffset)/stride {
			break
		}
		srcStart := row*stride + xOffset
		if srcStart >= len(data) {
			break
		}
		srcEnd := len(data)
		if rowBytes <= len(data)-srcStart {
			srcEnd = srcStart + rowBytes
		}
		dstOff := y * out.Stride
		copyBGRA(out.Pix[dstOff:dstOff+rowBytes], data[srcStart:srcEnd])
	}
	return out
}

func safePixelRowBytes(width int) (int, bool) {
	if width <= 0 || width > int(^uint(0)>>1)/4 {
		return 0, false
	}
	return width * 4, true
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
	if src == nil {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	if src.Bounds() == rect && rect.Min == (image.Point{0, 0}) {
		return src
	}
	width, height, ok := safePixelRectDimensions(rect)
	if !ok {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	out := image.NewRGBA(image.Rect(0, 0, width, height))
	r := rect.Intersect(src.Bounds())
	if r.Empty() {
		return out
	}
	if _, _, ok := safePixelRectDimensions(src.Rect); !ok || src.Stride <= 0 {
		return out
	}
	// dstX/dstY: top-left offset within out for the intersected region.
	dstX := r.Min.X - rect.Min.X
	dstY := r.Min.Y - rect.Min.Y
	w4, ok := safePixelRowBytes(r.Dx())
	if !ok {
		return out
	}
	for y := 0; y < r.Dy(); y++ {
		srcRow := r.Min.Y + y - src.Rect.Min.Y
		srcCol := r.Min.X - src.Rect.Min.X
		if srcRow < 0 || srcCol < 0 || srcRow > int(^uint(0)>>1)/src.Stride {
			continue
		}
		srcOff := srcRow * src.Stride
		if srcCol > int(^uint(0)>>1)/4 || srcOff > int(^uint(0)>>1)-srcCol*4 {
			continue
		}
		srcOff += srcCol * 4
		dstOff := (dstY+y)*out.Stride + dstX*4
		if srcOff < 0 || srcOff > len(src.Pix) || w4 > len(src.Pix)-srcOff || dstOff < 0 || dstOff > len(out.Pix) || w4 > len(out.Pix)-dstOff {
			continue
		}
		copy(out.Pix[dstOff:dstOff+w4], src.Pix[srcOff:srcOff+w4])
	}
	return out
}

func safePixelRectDimensions(rect image.Rectangle) (width, height int, ok bool) {
	if rect.Empty() {
		return 0, 0, true
	}
	width, ok = safePixelDimension(rect.Min.X, rect.Max.X)
	if !ok {
		return 0, 0, false
	}
	height, ok = safePixelDimension(rect.Min.Y, rect.Max.Y)
	if !ok {
		return 0, 0, false
	}
	rowBytes, ok := safePixelRowBytes(width)
	if !ok || height > int(^uint(0)>>1)/rowBytes {
		return 0, 0, false
	}
	return width, height, true
}

func safePixelDimension(min, max int) (int, bool) {
	if max < min {
		return 0, false
	}
	distance := uint64(max) - uint64(min)
	if distance > uint64(^uint(0)>>1) {
		return 0, false
	}
	return int(distance), true
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
