// Package find provides pixel-pattern scanning and waiting utilities.
// It depends only on the screen.Screenshotter interface and does not import
// any concrete backend. The hash function is pluggable (default: CRC32 IEEE).
package find

import (
	"bytes"
	"context"
	"fmt"
	"hash"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"time"
)

// Screenshotter is the subset of screen.Screenshotter needed by this package.
type Screenshotter interface {
	Grab(ctx context.Context, rect image.Rectangle) (image.Image, error)
	GrabFullHash(ctx context.Context) (uint32, error)
}

// Hasher returns a fresh hash.Hash32 for each call. Swap out for stronger
// hashing if CRC32 collisions become a practical concern.
type Hasher func() hash.Hash32

// DefaultHasher uses CRC32 IEEE.
var DefaultHasher Hasher = crc32.NewIEEE

// PixelHash computes a 32-bit hash of all RGBA pixels in img.
// For *image.RGBA images it uses a fast path that reads pixel bytes directly
// from the underlying Pix slice, avoiding per-pixel interface calls and
// colour-model conversions. Non-RGBA images are converted once to RGBA and
// then hashed using the same fast loop.
func PixelHash(img image.Image, newHash Hasher) uint32 {
	if newHash == nil {
		newHash = DefaultHasher
	}
	h := newHash()
	b := img.Bounds()

	// Fast path: direct Pix access for *image.RGBA.
	if rgba, ok := img.(*image.RGBA); ok {
		for y := b.Min.Y; y < b.Max.Y; y++ {
			off := (y-rgba.Rect.Min.Y)*rgba.Stride + (b.Min.X-rgba.Rect.Min.X)*4
			end := off + b.Dx()*4
			h.Write(rgba.Pix[off:end]) //nolint:errcheck
		}
		return h.Sum32()
	}

	// Convert non-RGBA images to RGBA once and then use the fast Pix path.
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		off := (y-rgba.Rect.Min.Y)*rgba.Stride + (b.Min.X-rgba.Rect.Min.X)*4
		end := off + b.Dx()*4
		h.Write(rgba.Pix[off:end]) //nolint:errcheck
	}
	return h.Sum32()
}

// GrabHash captures rect from sc and returns its pixel hash.
func GrabHash(ctx context.Context, sc Screenshotter, rect image.Rectangle, newHash Hasher) (uint32, error) {
	img, err := sc.Grab(ctx, rect)
	if err != nil {
		return 0, fmt.Errorf("find: grab: %w", err)
	}
	return PixelHash(img, newHash), nil
}

// FirstPixel returns the colour of the top-left pixel of rect captured from sc.
func FirstPixel(ctx context.Context, sc Screenshotter, rect image.Rectangle) (color.RGBA, error) {
	img, err := sc.Grab(ctx, image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Min.Y+1))
	if err != nil {
		return color.RGBA{}, fmt.Errorf("find: first pixel: %w", err)
	}
	b := img.Bounds()
	return color.RGBAModel.Convert(img.At(b.Min.X, b.Min.Y)).(color.RGBA), nil
}

// LastPixel returns the colour of the bottom-right pixel of rect captured from sc.
func LastPixel(ctx context.Context, sc Screenshotter, rect image.Rectangle) (color.RGBA, error) {
	x, y := rect.Max.X-1, rect.Max.Y-1
	img, err := sc.Grab(ctx, image.Rect(x, y, x+1, y+1))
	if err != nil {
		return color.RGBA{}, fmt.Errorf("find: last pixel: %w", err)
	}
	b := img.Bounds()
	return color.RGBAModel.Convert(img.At(b.Min.X, b.Min.Y)).(color.RGBA), nil
}

// Result pairs a hash with the rectangle it was captured from.
type Result struct {
	Hash uint32
	Rect image.Rectangle
}

// WaitFor polls rect every poll interval until its pixel hash equals want, or ctx expires.
// On success, it returns the final hash (which equals want). On timeout, it returns
// the last observed hash for debugging.
func WaitFor(ctx context.Context, sc Screenshotter, rect image.Rectangle, want uint32, poll time.Duration, newHash Hasher) (uint32, error) {
	for {
		h, err := GrabHash(ctx, sc, rect, newHash)
		if err != nil {
			return 0, err
		}
		if h == want {
			return h, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return h, fmt.Errorf("find: timeout waiting for hash %08x (last: %08x)", want, h)
		case <-timer.C:
		}
	}
}

// WaitForChange polls rect every poll interval until its hash differs from initial, or ctx expires.
// It pairs with WaitForNoChange: use WaitForChange to detect when a transition begins,
// then WaitForNoChange to detect when it ends.
func WaitForChange(ctx context.Context, sc Screenshotter, rect image.Rectangle, initial uint32, poll time.Duration, newHash Hasher) (uint32, error) {
	for {
		h, err := GrabHash(ctx, sc, rect, newHash)
		if err != nil {
			return 0, err
		}
		if h != initial {
			return h, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, fmt.Errorf("find: timeout waiting for change in rect %v (hash stable at %08x)", rect, initial)
		case <-timer.C:
		}
	}
}

// WaitForNoChange polls rect every poll interval until its pixel hash is unchanged for
// stable consecutive samples, then returns the stable hash. It is the counterpart to
// WaitForChange: use WaitForChange to detect when a transition begins (e.g. a click
// triggers a page load), then WaitForNoChange to detect when it finishes settling.
//
// stable must be ≥ 1. A value of 5 with poll=200ms means the region must look
// identical for one full second before returning.
func WaitForNoChange(ctx context.Context, sc Screenshotter, rect image.Rectangle, stable int, poll time.Duration, newHash Hasher) (uint32, error) {
	if stable <= 0 {
		stable = 1
	}
	var last uint32
	var sentinel color.RGBA
	sentinelSet := false
	streak := 0

	for {
		img, err := sc.Grab(ctx, rect)
		if err != nil {
			return 0, err
		}

		// Fast pixel check before full CRC32: if the top-left pixel of the
		// grabbed image changed since the last iteration, the hash is
		// definitely different — skip the CRC32 and reset the streak.
		// This is conservative: we only skip when we are certain of a change.
		b := img.Bounds()
		cur := color.RGBAModel.Convert(img.At(b.Min.X, b.Min.Y)).(color.RGBA)
		if sentinelSet && cur != sentinel {
			sentinel = cur
			last = 0 // force mismatch on next full hash
			streak = 0
			timer := time.NewTimer(poll)
			select {
			case <-ctx.Done():
				timer.Stop()
				return last, fmt.Errorf("find: WaitForNoChange timeout: region still changing after %d/%d stable samples (last hash %08x)", streak, stable, last)
			case <-timer.C:
			}
			continue
		}
		sentinel = cur
		sentinelSet = true

		h := PixelHash(img, newHash)
		if h == last {
			streak++
			if streak >= stable {
				return h, nil
			}
		} else {
			last = h
			streak = 1
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return last, fmt.Errorf("find: WaitForNoChange timeout: region still changing after %d/%d stable samples (last hash %08x)", streak, stable, last)
		case <-timer.C:
		}
	}
}

// ScanFor polls multiple regions in round-robin until one matches its expected hash,
// or ctx expires. rects and wants must be the same length; rects[i] is compared against
// wants[i]. Returns the first matching Result. This is useful for monitoring several
// independent UI regions (e.g. button states, dialog presence) simultaneously.
//
// When the regions are spatially compact (bounding-box area ≤ 2× the sum of individual
// rect areas), ScanFor performs a single sc.Grab of the union bounding box per poll
// cycle and hashes sub-regions in memory — reducing IPC round-trips from N to 1.
func ScanFor(ctx context.Context, sc Screenshotter, rects []image.Rectangle, wants []uint32, poll time.Duration, newHash Hasher) (Result, error) {
	if len(rects) != len(wants) {
		return Result{}, fmt.Errorf("find: ScanFor: len(rects)=%d != len(wants)=%d", len(rects), len(wants))
	}

	// Compute union bounding box and total individual area.
	bbox := rects[0]
	totalArea := 0
	for _, r := range rects {
		bbox = bbox.Union(r)
		totalArea += r.Dx() * r.Dy()
	}
	bboxArea := bbox.Dx() * bbox.Dy()
	useBbox := bboxArea <= 2*totalArea

	for {
		if useBbox {
			// Single grab covers all regions; hash sub-regions in memory.
			img, err := sc.Grab(ctx, bbox)
			if err != nil {
				return Result{}, err
			}
			sub, ok := img.(interface {
				SubImage(image.Rectangle) image.Image
			})
			if !ok {
				useBbox = false // fall back if SubImage unavailable
			} else {
				for i, rect := range rects {
					// Translate rect to grabbed image coordinate space.
					tr := image.Rect(
						rect.Min.X-bbox.Min.X,
						rect.Min.Y-bbox.Min.Y,
						rect.Max.X-bbox.Min.X,
						rect.Max.Y-bbox.Min.Y,
					)
					h := PixelHash(sub.SubImage(tr), newHash)
					if h == wants[i] {
						return Result{Hash: h, Rect: rect}, nil
					}
				}
			}
		}
		if !useBbox {
			for i, rect := range rects {
				h, err := GrabHash(ctx, sc, rect, newHash)
				if err != nil {
					return Result{}, err
				}
				if h == wants[i] {
					return Result{Hash: h, Rect: rect}, nil
				}
			}
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Result{}, fmt.Errorf("find: timeout scanning %d regions", len(rects))
		case <-timer.C:
		}
	}
}

// Anchor represents an absolute coordinate reference point on the screen.
type Anchor struct {
	X, Y int
}

// Rect returns a rectangle relative to the anchor's origin.
func (a Anchor) Rect(dx, dy, w, h int) image.Rectangle {
	return image.Rect(a.X+dx, a.Y+dy, a.X+dx+w, a.Y+dy+h)
}

// LocateExact performs an exact byte-for-byte search of reference within the searchArea.
// It returns the absolute image.Rectangle where it matches.
func LocateExact(ctx context.Context, sc Screenshotter, searchArea image.Rectangle, reference image.Image) (image.Rectangle, error) {
	src, err := sc.Grab(ctx, searchArea)
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("find: locate grab: %w", err)
	}

	sb := src.Bounds()
	rb := reference.Bounds()

	if rb.Dx() > sb.Dx() || rb.Dy() > sb.Dy() {
		return image.Rectangle{}, fmt.Errorf("find: reference image larger than search area")
	}

	// Precompute the top-left pixel of the reference. Most candidate positions
	// can be rejected with a single pixel comparison before calling matchAt
	// (which does rb.Dx() × rb.Dy() comparisons per position).
	refFirst := color.RGBAModel.Convert(reference.At(rb.Min.X, rb.Min.Y)).(color.RGBA)

	// Fast path: direct Pix access when both images are *image.RGBA.
	srcRGBA, srcOk := src.(*image.RGBA)
	refRGBA, refOk := reference.(*image.RGBA)
	if srcOk && refOk {
		// Read refFirst directly from Pix (avoids color model conversion in inner loop).
		refOff0 := (rb.Min.Y-refRGBA.Rect.Min.Y)*refRGBA.Stride + (rb.Min.X-refRGBA.Rect.Min.X)*4
		refFirst = color.RGBA{
			R: refRGBA.Pix[refOff0],
			G: refRGBA.Pix[refOff0+1],
			B: refRGBA.Pix[refOff0+2],
			A: refRGBA.Pix[refOff0+3],
		}
		for y := sb.Min.Y; y <= sb.Max.Y-rb.Dy(); y++ {
			for x := sb.Min.X; x <= sb.Max.X-rb.Dx(); x++ {
				srcOff := (y-srcRGBA.Rect.Min.Y)*srcRGBA.Stride + (x-srcRGBA.Rect.Min.X)*4
				p := srcRGBA.Pix[srcOff : srcOff+4]
				if p[0] != refFirst.R || p[1] != refFirst.G || p[2] != refFirst.B || p[3] != refFirst.A {
					continue
				}
				if matchAt(src, reference, x, y) {
					return image.Rect(x, y, x+rb.Dx(), y+rb.Dy()), nil
				}
			}
		}
		return image.Rectangle{}, fmt.Errorf("find: exact match not found")
	}

	for y := sb.Min.Y; y <= sb.Max.Y-rb.Dy(); y++ {
		for x := sb.Min.X; x <= sb.Max.X-rb.Dx(); x++ {
			if color.RGBAModel.Convert(src.At(x, y)).(color.RGBA) != refFirst {
				continue
			}
			if matchAt(src, reference, x, y) {
				return image.Rect(x, y, x+rb.Dx(), y+rb.Dy()), nil
			}
		}
	}
	return image.Rectangle{}, fmt.Errorf("find: exact match not found")
}

func matchAt(src, ref image.Image, ox, oy int) bool {
	rb := ref.Bounds()

	// Fast path: direct Pix row comparison for *image.RGBA images.
	// Avoids per-pixel interface dispatch and color model conversion.
	srcRGBA, srcOk := src.(*image.RGBA)
	refRGBA, refOk := ref.(*image.RGBA)
	if srcOk && refOk {
		w4 := rb.Dx() * 4
		for y := 0; y < rb.Dy(); y++ {
			srcOff := (oy+y-srcRGBA.Rect.Min.Y)*srcRGBA.Stride + (ox-srcRGBA.Rect.Min.X)*4
			refOff := (rb.Min.Y+y-refRGBA.Rect.Min.Y)*refRGBA.Stride + (rb.Min.X-refRGBA.Rect.Min.X)*4
			if !bytes.Equal(srcRGBA.Pix[srcOff:srcOff+w4], refRGBA.Pix[refOff:refOff+w4]) {
				return false
			}
		}
		return true
	}

	// Slow path: generic image via At() + colour model conversion.
	for y := 0; y < rb.Dy(); y++ {
		for x := 0; x < rb.Dx(); x++ {
			cSrc := color.RGBAModel.Convert(src.At(ox+x, oy+y)).(color.RGBA)
			cRef := color.RGBAModel.Convert(ref.At(rb.Min.X+x, rb.Min.Y+y)).(color.RGBA)
			if cSrc != cRef {
				return false
			}
		}
	}
	return true
}

// WaitWithTolerance pads expectedRect by radius pixels on all sides, captures the larger
// region, and performs an exact hash search of that size within it.
func WaitWithTolerance(ctx context.Context, sc Screenshotter, expectedRect image.Rectangle, targetHash uint32, radius int, poll time.Duration, newHash Hasher) (uint32, image.Rectangle, error) {
	if newHash == nil {
		newHash = DefaultHasher
	}
	searchArea := image.Rect(
		expectedRect.Min.X-radius,
		expectedRect.Min.Y-radius,
		expectedRect.Max.X+radius,
		expectedRect.Max.Y+radius,
	)

	for {
		img, err := sc.Grab(ctx, searchArea)
		if err != nil {
			return 0, image.Rectangle{}, fmt.Errorf("find: tolerance grab: %w", err)
		}

		sb := img.Bounds()
		w, h := expectedRect.Dx(), expectedRect.Dy()

		sub, ok := img.(interface {
			SubImage(r image.Rectangle) image.Image
		})
		if !ok {
			return 0, image.Rectangle{}, fmt.Errorf("find: grabbed image does not support SubImage")
		}
		for y := sb.Min.Y; y <= sb.Max.Y-h; y++ {
			for x := sb.Min.X; x <= sb.Max.X-w; x++ {
				r := image.Rect(x, y, x+w, y+h)
				subImg := sub.SubImage(r)
				hVal := PixelHash(subImg, newHash)
				if hVal == targetHash {
					return targetHash, r, nil
				}
			}
		}

		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return 0, image.Rectangle{}, fmt.Errorf("find: timeout waiting for tolerance match")
		case <-timer.C:
		}
	}
}

// PixelFound scans img (which was captured for rect) for the first pixel
// whose colour is within tolerance of target. Returns the absolute screen
// coordinate and true if found.
func PixelFound(img image.Image, rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, bool) {
	b := img.Bounds()

	// Fast path: read directly from Pix for *image.RGBA, avoiding per-pixel
	// At() calls and colour model conversion.
	if rgba, ok := img.(*image.RGBA); ok {
		for y := b.Min.Y; y < b.Max.Y; y++ {
			off := (y-rgba.Rect.Min.Y)*rgba.Stride + (b.Min.X-rgba.Rect.Min.X)*4
			for x := b.Min.X; x < b.Max.X; x++ {
				p := rgba.Pix[off : off+4]
				if abs(int(p[0])-int(target.R)) <= tolerance &&
					abs(int(p[1])-int(target.G)) <= tolerance &&
					abs(int(p[2])-int(target.B)) <= tolerance {
					return image.Pt(rect.Min.X+x-b.Min.X, rect.Min.Y+y-b.Min.Y), true
				}
				off += 4
			}
		}
		return image.Point{}, false
	}

	// Slow path: generic image via At() + colour model conversion.
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			if colorClose(c, target, tolerance) {
				return image.Pt(rect.Min.X+x-b.Min.X, rect.Min.Y+y-b.Min.Y), true
			}
		}
	}
	return image.Point{}, false
}

// FindColor scans rect for the first pixel whose colour is within tolerance of
// target. Returns the absolute (x, y) of the match. Tolerance is applied per
// channel: |r-r'| ≤ tol && |g-g'| ≤ tol && |b-b'| ≤ tol.
func FindColor(ctx context.Context, sc Screenshotter, rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	img, err := sc.Grab(ctx, rect)
	if err != nil {
		return image.Point{}, fmt.Errorf("find: find-color grab: %w", err)
	}
	if p, ok := PixelFound(img, rect, target, tolerance); ok {
		return p, nil
	}
	return image.Point{}, fmt.Errorf("find: colour #%02x%02x%02x not found (tolerance=%d)", target.R, target.G, target.B, tolerance)
}

func colorClose(a, b color.RGBA, tol int) bool {
	return abs(int(a.R)-int(b.R)) <= tol &&
		abs(int(a.G)-int(b.G)) <= tol &&
		abs(int(a.B)-int(b.B)) <= tol
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// WaitForLocate polls searchArea every poll interval until reference is found
// via exact pixel matching, or ctx expires. Returns the absolute rectangle
// where the reference was located.
func WaitForLocate(ctx context.Context, sc Screenshotter, searchArea image.Rectangle, reference image.Image, poll time.Duration) (image.Rectangle, error) {
	for {
		r, err := LocateExact(ctx, sc, searchArea, reference)
		if err == nil {
			return r, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return image.Rectangle{}, fmt.Errorf("find: timeout waiting to locate reference image: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// WaitForFn polls rect every poll interval until fn returns true for the
// grabbed image, or ctx expires. fn receives the raw grabbed image each
// iteration and may inspect it with any predicate (brightness, color
// presence, histogram, etc.).
func WaitForFn(ctx context.Context, sc Screenshotter, rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	for {
		img, err := sc.Grab(ctx, rect)
		if err != nil {
			return nil, err
		}
		if fn(img) {
			return img, nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("find: WaitForFn timeout: predicate never satisfied for rect %v", rect)
		case <-timer.C:
		}
	}
}
