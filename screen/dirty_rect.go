package screen

import (
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/internal/util"
)

// dirtyRects compares packed 8-bit RGBA or NRGBA captures without hashing.
// Runs of changed pixels are merged vertically when their horizontal spans
// match exactly. Output is in image coordinates, ordered by first occurrence.
// A resync has no partial rectangles. Different valid geometry or pixel
// representation requires resync; malformed storage is an error.
func dirtyRects(before, after image.Image, maxRects int) (rects []image.Rectangle, resync bool, err error) {
	if maxRects <= 0 {
		return nil, false, fmt.Errorf("screen: invalid dirty rectangle limit")
	}
	a, err := dirtyFrame(before)
	if err != nil {
		return nil, false, err
	}
	b, err := dirtyFrame(after)
	if err != nil {
		return nil, false, err
	}
	if a.bounds != b.bounds || a.straight != b.straight {
		return nil, true, nil
	}
	// Only rectangles ending at the preceding row can be extended. A map
	// indexed by horizontal span avoids quadratic searches and is bounded by
	// the same limit as the result.
	type span struct{ lo, hi int }
	prev := make(map[span]int)
	next := make(map[span]int)
	w, h := a.bounds.Dx(), a.bounds.Dy()
	for y := 0; y < h; y++ {
		ar := a.pix[y*a.stride : y*a.stride+w*4]
		br := b.pix[y*b.stride : y*b.stride+w*4]
		clear(next)
		for x := 0; x < w; {
			if equalDirtyPixel(ar, br, x) {
				x++
				continue
			}
			start := x
			for x < w && !equalDirtyPixel(ar, br, x) {
				x++
			}
			s := span{start, x}
			if i, ok := prev[s]; ok {
				rects[i].Max.Y++
				next[s] = i
				continue
			}
			if len(rects) == maxRects {
				return nil, true, nil
			}
			next[s] = len(rects)
			rects = append(rects, image.Rect(a.bounds.Min.X+start, a.bounds.Min.Y+y, a.bounds.Min.X+x, a.bounds.Min.Y+y+1))
		}
		prev, next = next, prev
	}
	return rects, false, nil
}

func equalDirtyPixel(a, b []byte, x int) bool {
	i := x * 4
	return a[i] == b[i] && a[i+1] == b[i+1] && a[i+2] == b[i+2] && a[i+3] == b[i+3]
}

type dirtyFrameView struct {
	bounds   image.Rectangle
	pix      []byte
	stride   int
	straight bool
}

// Restrict this core to known packed capture layouts: arbitrary image.Image
// implementations cannot attest their backing storage before pixel access.
func dirtyFrame(img image.Image) (dirtyFrameView, error) {
	var f dirtyFrameView
	if util.IsNil(img) {
		return f, fmt.Errorf("screen: nil dirty frame")
	}
	switch p := img.(type) {
	case *image.RGBA:
		f = dirtyFrameView{p.Rect, p.Pix, p.Stride, false}
	case *image.NRGBA:
		f = dirtyFrameView{p.Rect, p.Pix, p.Stride, true}
	default:
		return f, fmt.Errorf("screen: unsupported dirty frame layout")
	}
	w, h, ok := safePixelRectDimensions(f.bounds)
	row, rowOK := safePixelRowBytes(w)
	// Division avoids overflow even for a malicious stride or height.
	if !ok || !rowOK || h <= 0 || f.stride < row || len(f.pix) < row ||
		h-1 > (len(f.pix)-row)/f.stride {
		return dirtyFrameView{}, fmt.Errorf("screen: invalid dirty frame geometry or storage")
	}
	return f, nil
}
