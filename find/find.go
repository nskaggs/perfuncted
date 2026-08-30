// Package find provides pixel-pattern scanning and waiting utilities.
// It depends only on the screen.Screenshotter interface and does not import
// any concrete backend. The hash function is pluggable (default: CRC32 IEEE).
package find

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"time"

	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/util"
	pollpkg "github.com/nskaggs/perfuncted/poll"
)

// ErrNotFound is returned when a pixel pattern or color could not be located.
var ErrNotFound = errors.New("not found")

// Screenshotter is the subset of screen.Screenshotter needed by this package.
type Screenshotter interface {
	Grab(ctx context.Context, rect image.Rectangle) (image.Image, error)
}

// Hasher returns a fresh hash.Hash32 for each call. Swap out for stronger
// hashing if CRC32 collisions become a practical concern.
type Hasher func() hash.Hash32

// DefaultHasher uses CRC32 IEEE.
var DefaultHasher Hasher = crc32.NewIEEE

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func checkAvailable(sc Screenshotter) error {
	if util.IsNil(sc) {
		return fmt.Errorf("find: screen backend not available")
	}
	return nil
}

func checkImage(img image.Image, operation string) error {
	if util.IsNil(img) {
		return fmt.Errorf("find: %s returned nil image", operation)
	}
	return nil
}

// PixelHash computes a 32-bit hash of all RGBA pixels in img.
// For *image.RGBA images it uses a fast path that reads pixel bytes directly
// from the underlying Pix slice, avoiding per-pixel interface calls and
// colour-model conversions. Non-RGBA images are converted once to RGBA and
// then hashed using the same fast loop.
func PixelHash(img image.Image, newHash Hasher) uint32 {
	if checkImage(img, "hash") != nil {
		return 0
	}
	return pixelHashImage(img, img.Bounds(), newHash)
}

// pixelHashImage hashes rect from img without creating a subimage for the
// RGBA fast path. Keeping the rectangle in the source image's coordinate
// space lets callers hash compact regions without allocating a temporary
// *image.RGBA for each one.
func pixelHashImage(img image.Image, rect image.Rectangle, newHash Hasher) uint32 {
	if newHash == nil {
		newHash = DefaultHasher
	}
	h := newHash()
	b := rect.Intersect(img.Bounds())

	// Fast path: direct Pix access for *image.RGBA.
	if rgba, ok := img.(*image.RGBA); ok {
		if b.Empty() {
			return h.Sum32()
		}
		// Optimization: if the visible rows are contiguous, hash them at once.
		// A full-width subimage can have trailing rows in Pix, so bound the slice
		// to the image's height rather than hashing the entire backing buffer.
		if b == rgba.Rect && rgba.Stride == b.Dx()*4 && len(rgba.Pix) >= rgba.Stride*b.Dy() {
			h.Write(rgba.Pix[:rgba.Stride*b.Dy()])
			return h.Sum32()
		}
		safe := true
		for y := b.Min.Y; y < b.Max.Y; y++ {
			off := (y-rgba.Rect.Min.Y)*rgba.Stride + (b.Min.X-rgba.Rect.Min.X)*4
			end := off + b.Dx()*4
			if off < 0 || end > len(rgba.Pix) {
				safe = false
				break
			}
			h.Write(rgba.Pix[off:end]) //nolint:errcheck
		}
		if safe {
			return h.Sum32()
		}
		return 0
	}

	// Convert non-RGBA images to RGBA once and then use the fast contiguous Pix path.
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)
	h.Write(rgba.Pix)
	return h.Sum32()
}

// GrabHash captures rect from sc and returns the same hash as PixelHash on the
// grabbed image. Keeping this path canonical ensures all backends use the same
// pixel representation, including the default hash.
func GrabHash(ctx context.Context, sc Screenshotter, rect image.Rectangle, newHash Hasher) (uint32, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return 0, err
	}
	img, err := sc.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	if err := checkImage(img, "grab"); err != nil {
		return 0, err
	}
	return PixelHash(img, newHash), nil
}

// FirstPixel returns the colour of the top-left pixel of rect captured from sc.
func FirstPixel(ctx context.Context, sc Screenshotter, rect image.Rectangle) (color.RGBA, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return color.RGBA{}, err
	}
	img, err := sc.Grab(ctx, image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Min.Y+1))
	if err != nil {
		return color.RGBA{}, fmt.Errorf("find: first pixel: %w", err)
	}
	if err := checkImage(img, "first pixel grab"); err != nil {
		return color.RGBA{}, err
	}
	if err := contextErr(ctx); err != nil {
		return color.RGBA{}, err
	}
	b := img.Bounds()
	if b.Empty() {
		return color.RGBA{}, fmt.Errorf("find: first pixel grab returned empty image")
	}
	return color.RGBAModel.Convert(img.At(b.Min.X, b.Min.Y)).(color.RGBA), nil //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA
}

// Result pairs a hash with the rectangle it was captured from.
type Result struct {
	// Hash is the matching pixel hash.
	Hash uint32
	// Rect is the rectangle that produced Hash.
	Rect image.Rectangle
}

func poll(ctx context.Context, pollDur time.Duration, onCancel uint32, fn func(attempt int) (done bool, result uint32, err error)) (uint32, error) { //nolint:gocyclo
	ctx = contextutil.Default(ctx)
	if pollDur <= 0 {
		attempt := 0
		var t *time.Timer
		for {
			if err := contextErr(ctx); err != nil {
				return onCancel, err
			}
			done, res, err := fn(attempt)
			if err != nil {
				return res, err
			}
			if err := contextErr(ctx); err != nil {
				return res, err
			}
			if done {
				return res, nil
			}
			d := pollpkg.AdaptivePoll(attempt, 10*time.Millisecond, 200*time.Millisecond)
			attempt++
			if t == nil {
				t = time.NewTimer(d)
			} else {
				t.Reset(d)
			}
			select {
			case <-ctx.Done():
				t.Stop()
				return res, ctx.Err()
			case <-t.C:
			}
		}
	}

	ticker := time.NewTicker(pollpkg.Clamp(pollDur))
	defer ticker.Stop()
	for {
		if err := contextErr(ctx); err != nil {
			return onCancel, err
		}
		done, res, err := fn(0)
		if err != nil {
			return res, err
		}
		if err := contextErr(ctx); err != nil {
			return res, err
		}
		if done {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-ticker.C:
		}
	}
}

// WaitFor polls rect every poll interval until its pixel hash equals want, or ctx expires.
// On success, it returns the final hash (which equals want). On timeout, it returns
// the last observed hash for debugging.
func WaitFor(ctx context.Context, sc Screenshotter, rect image.Rectangle, want uint32, pollDur time.Duration, newHash Hasher) (uint32, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return 0, err
	}
	h, err := poll(ctx, pollDur, 0, func(attempt int) (bool, uint32, error) {
		h, err := GrabHash(ctx, sc, rect, newHash)
		if err != nil {
			return false, 0, err
		}
		if h == want {
			return true, h, nil
		}
		return false, h, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return h, fmt.Errorf("find: timeout waiting for hash %08x (last: %08x): %w", want, h, ctx.Err())
		}
		return 0, err
	}
	return h, nil
}

// WaitForChange polls rect every poll interval until its hash differs from initial, or ctx expires.
// It pairs with WaitForNoChange: use WaitForChange to detect when a transition begins,
// then WaitForNoChange to detect when it ends.
func WaitForChange(ctx context.Context, sc Screenshotter, rect image.Rectangle, initial uint32, pollDur time.Duration, newHash Hasher) (uint32, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return 0, err
	}
	h, err := poll(ctx, pollDur, initial, func(attempt int) (bool, uint32, error) {
		h, err := GrabHash(ctx, sc, rect, newHash)
		if err != nil {
			return false, 0, err
		}
		if h != initial {
			return true, h, nil
		}
		return false, h, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return h, fmt.Errorf("find: timeout waiting for change in rect %v (hash stable at %08x): %w", rect, initial, ctx.Err())
		}
		return 0, err
	}
	return h, nil
}

// WaitForNoChange polls rect every poll interval until its pixel hash is unchanged for
// stable consecutive samples, then returns the stable hash. It is the counterpart to
// WaitForChange: use WaitForChange to detect when a transition begins (e.g. a click
// triggers a page load), then WaitForNoChange to detect when it finishes settling.
//
// stable must be ≥ 1. A value of 5 with poll=200ms means the region must look
// identical for one full second before returning.
func WaitForNoChange(ctx context.Context, sc Screenshotter, rect image.Rectangle, stable int, poll time.Duration, newHash Hasher) (uint32, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return 0, err
	}
	return WaitForNoChangeFrom(ctx, sc, rect, 0, stable, poll, newHash)
}

// WaitForNoChangeFrom is the same as WaitForNoChange but accepts an initial hash
// to avoid the first capture if the caller already knows the current state.
// If initial is 0, the first capture is performed immediately.
func WaitForNoChangeFrom(ctx context.Context, sc Screenshotter, rect image.Rectangle, initial uint32, stable int, pollDur time.Duration, newHash Hasher) (uint32, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return 0, err
	}
	if stable <= 0 {
		stable = 1
	}
	last := initial
	streak := 0
	if initial != 0 {
		streak = 1
	}
	var sentinel color.RGBA
	sentinelSet := false

	h, err := poll(ctx, pollDur, last, func(attempt int) (bool, uint32, error) {
		img, err := sc.Grab(ctx, rect)
		if err != nil {
			return false, 0, err
		}
		if err := checkImage(img, "wait-for-no-change grab"); err != nil {
			return false, 0, err
		}

		// Fast pixel check before full CRC32: if the top-left pixel of the
		// grabbed image changed since the last iteration, the hash is
		// definitely different — skip the CRC32 and reset the streak.
		// This is conservative: we only skip when we are certain of a change.
		b := img.Bounds()
		if b.Empty() {
			return false, 0, fmt.Errorf("find: wait-for-no-change grab returned empty image")
		}
		cur := color.RGBAModel.Convert(img.At(b.Min.X, b.Min.Y)).(color.RGBA) //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA
		if sentinelSet && cur != sentinel {
			sentinel = cur
			streak = 0
			// We skip the hash but must continue polling until stable.
			return false, last, nil
		}
		sentinel = cur
		sentinelSet = true

		h := PixelHash(img, newHash)
		if streak > 0 && h == last {
			streak++
			if streak >= stable {
				return true, h, nil
			}
		} else {
			last = h
			streak = 1
		}
		return false, last, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return h, fmt.Errorf("find: WaitForNoChange timeout: region still changing after %d/%d stable samples (last hash %08x): %w", streak, stable, last, ctx.Err())
		}
		return 0, err
	}
	return h, nil
}

// ScanFor polls multiple regions in round-robin until one matches its expected hash,
// or ctx expires. rects and wants must be the same length; rects[i] is compared against
// wants[i]. Returns the first matching Result. This is useful for monitoring several
// independent UI regions (e.g. button states, dialog presence) simultaneously.
//
// When the regions are spatially compact (bounding-box area ≤ 2× the sum of individual
// rect areas), ScanFor performs a single sc.Grab of the union bounding box per poll
// cycle and hashes sub-regions in memory — reducing IPC round-trips from N to 1.
func ScanFor(ctx context.Context, sc Screenshotter, rects []image.Rectangle, wants []uint32, poll time.Duration, newHash Hasher) (Result, error) { //nolint:gocyclo
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return Result{}, err
	}
	if len(rects) != len(wants) {
		return Result{}, fmt.Errorf("find: ScanFor: len(rects)=%d != len(wants)=%d", len(rects), len(wants))
	}
	if len(rects) == 0 {
		return Result{}, fmt.Errorf("find: ScanFor: no regions provided")
	}
	for i, r := range rects {
		if r.Empty() {
			return Result{}, fmt.Errorf("find: ScanFor: empty region at index %d: %v", i, r)
		}
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

	// A match on the first scan is common. Defer timer allocation until a
	// second poll is actually needed so the fast path does not pay for a
	// ticker it will never read.
	var ticker *time.Ticker
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	for {
		if err := contextErr(ctx); err != nil {
			return Result{}, err
		}
		if useBbox {
			// Single grab covers all regions; hash sub-regions in memory.
			img, err := sc.Grab(ctx, bbox)
			if err != nil {
				return Result{}, err
			}
			if err := checkImage(img, "scan grab"); err != nil {
				return Result{}, err
			}
			if err := contextErr(ctx); err != nil {
				return Result{}, err
			}
			imgBase := img.Bounds().Min
			for i, rect := range rects {
				// Translate the requested screen rect into the grabbed image's
				// coordinate space. Backends may return either zero-origin or
				// absolute-bounds images, so respect the returned bounds.
				tr := image.Rect(
					rect.Min.X-bbox.Min.X+imgBase.X,
					rect.Min.Y-bbox.Min.Y+imgBase.Y,
					rect.Max.X-bbox.Min.X+imgBase.X,
					rect.Max.Y-bbox.Min.Y+imgBase.Y,
				)
				h, ok := pixelHashScanRegion(img, tr, newHash)
				if !ok {
					useBbox = false // fall back if SubImage unavailable
					break
				}
				if err := contextErr(ctx); err != nil {
					return Result{}, err
				}
				if h == wants[i] {
					return Result{Hash: h, Rect: rect}, nil
				}
			}
		}
		if !useBbox {
			for i, rect := range rects {
				h, err := GrabHash(ctx, sc, rect, newHash)
				if err != nil {
					return Result{}, err
				}
				if err := contextErr(ctx); err != nil {
					return Result{}, err
				}
				if h == wants[i] {
					return Result{Hash: h, Rect: rect}, nil
				}
			}
		}
		if ticker == nil {
			ticker = time.NewTicker(pollpkg.Clamp(poll))
		}
		select {
		case <-ctx.Done():
			return Result{}, fmt.Errorf("find: timeout scanning %d regions: %w", len(rects), ctx.Err())
		case <-ticker.C:
		}
	}
}

func pixelHashScanRegion(img image.Image, rect image.Rectangle, newHash Hasher) (uint32, bool) {
	if _, ok := img.(*image.RGBA); ok {
		return pixelHashImage(img, rect, newHash), true
	}
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return 0, false
	}
	return PixelHash(sub.SubImage(rect), newHash), true
}

// Anchor represents an absolute coordinate reference point on the screen.
type Anchor struct {
	// X is the horizontal screen coordinate.
	X, Y int
}

// LocateExactInImage performs an exact byte-for-byte search of reference within src.
// searchArea describes the screen-area that src was captured from, so returned
// coordinates can be translated back to screen space. This avoids the caller
// having to perform a second Grab.
func LocateExactInImage(src image.Image, searchArea image.Rectangle, reference image.Image) (image.Rectangle, error) { //nolint:gocyclo
	if searchArea.Empty() {
		return image.Rectangle{}, fmt.Errorf("find: locate search area is empty")
	}
	if err := checkImage(src, "locate source"); err != nil {
		return image.Rectangle{}, err
	}
	if err := checkImage(reference, "locate reference"); err != nil {
		return image.Rectangle{}, err
	}
	if reference.Bounds().Empty() {
		return image.Rectangle{}, fmt.Errorf("find: reference image is empty")
	}

	sb := src.Bounds()
	rb := reference.Bounds()

	if rb.Dx() > sb.Dx() || rb.Dy() > sb.Dy() {
		return image.Rectangle{}, fmt.Errorf("find: reference image larger than search area")
	}

	// Precompute the top-left pixel of the reference. Most candidate positions
	// can be rejected with a single pixel comparison before calling matchAt
	// (which does rb.Dx() × rb.Dy() comparisons per position).
	refFirst := color.RGBAModel.Convert(reference.At(rb.Min.X, rb.Min.Y)).(color.RGBA) //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA

	// Fast path: direct Pix access when both images are *image.RGBA.
	srcRGBA, srcOk := src.(*image.RGBA)
	refRGBA, refOk := reference.(*image.RGBA)
	if srcOk && refOk {
		// Read the reference bytes directly from Pix (avoids color model conversion in inner loop).
		refOff0 := (rb.Min.Y-refRGBA.Rect.Min.Y)*refRGBA.Stride + (rb.Min.X-refRGBA.Rect.Min.X)*4
		if refOff0 >= 0 && refOff0+4 <= len(refRGBA.Pix) && srcRGBA.Stride >= sb.Dx()*4 {
			refFirstBytes := refRGBA.Pix[refOff0 : refOff0+4]
			for y := sb.Min.Y; y <= sb.Max.Y-rb.Dy(); y++ {
				rowStart := (y-srcRGBA.Rect.Min.Y)*srcRGBA.Stride + (sb.Min.X-srcRGBA.Rect.Min.X)*4
				rowEnd := rowStart + sb.Dx()*4
				if rowStart < 0 || rowEnd > len(srcRGBA.Pix) {
					break
				}
				row := srcRGBA.Pix[rowStart:rowEnd]
				for from := 0; from < len(row); {
					offset := bytes.Index(row[from:], refFirstBytes)
					if offset < 0 {
						break
					}
					pixelOffset := from + offset
					if pixelOffset%4 == 0 {
						x := sb.Min.X + pixelOffset/4
						if x <= sb.Max.X-rb.Dx() && matchAt(src, reference, x, y) {
							return translateRect(image.Rect(x, y, x+rb.Dx(), y+rb.Dy()), sb.Min, searchArea.Min), nil
						}
					}
					from = pixelOffset + 1
				}
			}
			return image.Rectangle{}, fmt.Errorf("%w: exact match", ErrNotFound)
		}
	}

	for y := sb.Min.Y; y <= sb.Max.Y-rb.Dy(); y++ {
		for x := sb.Min.X; x <= sb.Max.X-rb.Dx(); x++ {
			if color.RGBAModel.Convert(src.At(x, y)).(color.RGBA) != refFirst { //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA
				continue
			}
			if matchAt(src, reference, x, y) {
				return translateRect(image.Rect(x, y, x+rb.Dx(), y+rb.Dy()), sb.Min, searchArea.Min), nil
			}
		}
	}
	return image.Rectangle{}, fmt.Errorf("%w: exact match", ErrNotFound)
}

// LocateExact performs an exact byte-for-byte search of reference within the searchArea.
// It returns the absolute image.Rectangle where it matches.
func LocateExact(ctx context.Context, sc Screenshotter, searchArea image.Rectangle, reference image.Image) (image.Rectangle, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return image.Rectangle{}, err
	}
	src, err := sc.Grab(ctx, searchArea)
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("find: locate grab: %w", err)
	}
	if err := contextErr(ctx); err != nil {
		return image.Rectangle{}, err
	}
	return LocateExactInImage(src, searchArea, reference)
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
			if srcOff < 0 || srcOff+w4 > len(srcRGBA.Pix) || refOff < 0 || refOff+w4 > len(refRGBA.Pix) {
				return false
			}
			if !bytes.Equal(srcRGBA.Pix[srcOff:srcOff+w4], refRGBA.Pix[refOff:refOff+w4]) {
				return false
			}
		}
		return true
	}

	// Slow path: generic image via At() + colour model conversion.
	for y := 0; y < rb.Dy(); y++ {
		for x := 0; x < rb.Dx(); x++ {
			cSrc := color.RGBAModel.Convert(src.At(ox+x, oy+y)).(color.RGBA)             //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA
			cRef := color.RGBAModel.Convert(ref.At(rb.Min.X+x, rb.Min.Y+y)).(color.RGBA) //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA
			if cSrc != cRef {
				return false
			}
		}
	}
	return true
}

func translateRect(r image.Rectangle, fromMin, toMin image.Point) image.Rectangle {
	delta := toMin.Sub(fromMin)
	return r.Add(delta)
}

// PixelFound scans img (which was captured for rect) for the first pixel
// whose colour is within tolerance of target. Returns the absolute screen
// coordinate and true if found.
func PixelFound(img image.Image, rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, bool) { //nolint:gocyclo
	if checkImage(img, "pixel scan") != nil {
		return image.Point{}, false
	}
	b := img.Bounds()

	// Fast path: read directly from Pix for *image.RGBA, avoiding per-pixel
	// At() calls and colour model conversion.
	if rgba, ok := img.(*image.RGBA); ok {
		return scanPackedPixels(rgba.Pix, rgba.Rect, rgba.Stride, rect, target, tolerance)
	}

	// NRGBA is the native layout returned by several screenshot backends.
	// Scan its Pix buffer directly; calling At and converting through
	// color.RGBAModel allocates for every pixel.
	if nrgba, ok := img.(*image.NRGBA); ok {
		return scanPackedPixels(nrgba.Pix, nrgba.Rect, nrgba.Stride, rect, target, tolerance)
	}

	// Slow path: generic image via At() + colour model conversion.
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA) //nolint:errcheck // color.RGBAModel.Convert always returns color.RGBA
			if colorClose(c, target, tolerance) {
				return image.Pt(rect.Min.X+x-b.Min.X, rect.Min.Y+y-b.Min.Y), true
			}
		}
	}
	return image.Point{}, false
}

func scanPackedPixels(
	pix []byte,
	bounds image.Rectangle,
	stride int,
	rect image.Rectangle,
	target color.RGBA,
	tolerance int,
) (image.Point, bool) {
	if bounds.Empty() || stride < bounds.Dx()*4 {
		return image.Point{}, false
	}
	minRequired := (bounds.Dy()-1)*stride + bounds.Dx()*4
	if len(pix) < minRequired {
		return image.Point{}, false
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		off := (y - bounds.Min.Y) * stride
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_ = pix[off+3] // eliminate bounds check
			if (tolerance == 0 &&
				pix[off] == target.R &&
				pix[off+1] == target.G &&
				pix[off+2] == target.B) ||
				(tolerance != 0 &&
					abs(int(pix[off])-int(target.R)) <= tolerance &&
					abs(int(pix[off+1])-int(target.G)) <= tolerance &&
					abs(int(pix[off+2])-int(target.B)) <= tolerance) {
				return image.Pt(rect.Min.X+x-bounds.Min.X, rect.Min.Y+y-bounds.Min.Y), true
			}
			off += 4
		}
	}
	return image.Point{}, false
}

// FindColor scans rect for the first pixel whose colour is within tolerance of
// target. Returns the absolute (x, y) of the match. Tolerance is applied per
// channel: |r-r'| ≤ tol && |g-g'| ≤ tol && |b-b'| ≤ tol.
func FindColor(ctx context.Context, sc Screenshotter, rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) { //nolint:revive // exported API name is intentional
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return image.Point{}, err
	}
	if tolerance < 0 || tolerance > 255 {
		return image.Point{}, fmt.Errorf("find: invalid tolerance %d (want 0..255)", tolerance)
	}
	img, err := sc.Grab(ctx, rect)
	if err != nil {
		return image.Point{}, fmt.Errorf("find: find-color grab: %w", err)
	}
	if err := checkImage(img, "find-color grab"); err != nil {
		return image.Point{}, err
	}
	if err := contextErr(ctx); err != nil {
		return image.Point{}, err
	}
	if p, ok := PixelFound(img, rect, target, tolerance); ok {
		return p, nil
	}
	return image.Point{}, fmt.Errorf("%w: colour #%02x%02x%02x (tolerance=%d)", ErrNotFound, target.R, target.G, target.B, tolerance)
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
func WaitForLocate(ctx context.Context, sc Screenshotter, searchArea image.Rectangle, reference image.Image, pollDur time.Duration) (image.Rectangle, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return image.Rectangle{}, err
	}
	var foundRect image.Rectangle
	_, err := poll(ctx, pollDur, 0, func(attempt int) (bool, uint32, error) {
		r, err := LocateExact(ctx, sc, searchArea, reference)
		if err == nil {
			foundRect = r
			return true, 0, nil
		}
		if errors.Is(err, ErrNotFound) {
			return false, 0, nil
		}
		return false, 0, err
	})
	if err != nil {
		if ctx.Err() != nil {
			return image.Rectangle{}, fmt.Errorf("find: timeout waiting to locate reference image: %w", ctx.Err())
		}
		return image.Rectangle{}, err
	}
	return foundRect, nil
}

// WaitForFn polls rect every poll interval until fn returns true for the
// grabbed image, or ctx expires. fn receives ctx and the raw grabbed image
// each iteration so it can respect cancellation and inspect the image with
// any predicate (brightness, color presence, histogram, etc.).
func WaitForFn(ctx context.Context, sc Screenshotter, rect image.Rectangle, fn func(context.Context, image.Image) bool, pollDur time.Duration) (image.Image, error) {
	ctx = contextutil.Default(ctx)
	if err := checkAvailable(sc); err != nil {
		return nil, err
	}
	var foundImg image.Image
	_, err := poll(ctx, pollDur, 0, func(attempt int) (bool, uint32, error) {
		img, err := sc.Grab(ctx, rect)
		if err != nil {
			return false, 0, err
		}
		if err := checkImage(img, "predicate grab"); err != nil {
			return false, 0, err
		}
		if fn(ctx, img) {
			foundImg = img
			return true, 0, nil
		}
		return false, 0, nil
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("find: WaitForFn timeout: predicate never satisfied for rect %v: %w", rect, ctx.Err())
		}
		return nil, err
	}
	return foundImg, nil
}
