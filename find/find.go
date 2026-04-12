// Package find provides pixel-pattern scanning and waiting utilities.
// It depends only on the screen.Screenshotter interface and does not import
// any concrete backend. The hash function is pluggable (default: CRC32 IEEE).
package find

import (
	"context"
	"fmt"
	"hash"
	"hash/crc32"
	"image"
	"image/color"
	"time"
)

// Screenshotter is the subset of screen.Screenshotter needed by this package.
type Screenshotter interface {
	Grab(rect image.Rectangle) (image.Image, error)
}

// Hasher returns a fresh hash.Hash32 for each call. Swap out for stronger
// hashing if CRC32 collisions become a practical concern.
type Hasher func() hash.Hash32

// DefaultHasher uses CRC32 IEEE.
var DefaultHasher Hasher = func() hash.Hash32 { return crc32.NewIEEE() }

// PixelHash computes a 32-bit hash of all RGBA pixels in img.
func PixelHash(img image.Image, newHash Hasher) uint32 {
	if newHash == nil {
		newHash = DefaultHasher
	}
	h := newHash()
	b := img.Bounds()
	buf := make([]byte, 4)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			buf[0], buf[1], buf[2], buf[3] = c.R, c.G, c.B, c.A
			h.Write(buf) //nolint:errcheck
		}
	}
	return h.Sum32()
}

// GrabHash captures rect from sc and returns its pixel hash.
func GrabHash(sc Screenshotter, rect image.Rectangle, newHash Hasher) (uint32, error) {
	img, err := sc.Grab(rect)
	if err != nil {
		return 0, fmt.Errorf("find: grab: %w", err)
	}
	return PixelHash(img, newHash), nil
}

// FirstPixel returns the colour of the top-left pixel of rect captured from sc.
func FirstPixel(sc Screenshotter, rect image.Rectangle) (color.RGBA, error) {
	img, err := sc.Grab(image.Rect(rect.Min.X, rect.Min.Y, rect.Min.X+1, rect.Min.Y+1))
	if err != nil {
		return color.RGBA{}, fmt.Errorf("find: first pixel: %w", err)
	}
	return color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA), nil
}

// LastPixel returns the colour of the bottom-right pixel of rect captured from sc.
func LastPixel(sc Screenshotter, rect image.Rectangle) (color.RGBA, error) {
	x, y := rect.Max.X-1, rect.Max.Y-1
	img, err := sc.Grab(image.Rect(x, y, x+1, y+1))
	if err != nil {
		return color.RGBA{}, fmt.Errorf("find: last pixel: %w", err)
	}
	return color.RGBAModel.Convert(img.At(0, 0)).(color.RGBA), nil
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
		h, err := GrabHash(sc, rect, newHash)
		if err != nil {
			return 0, err
		}
		if h == want {
			return h, nil
		}
		select {
		case <-ctx.Done():
			return h, fmt.Errorf("find: timeout waiting for hash %08x (last: %08x)", want, h)
		case <-time.After(poll):
		}
	}
}

// WaitForChange polls rect every poll interval until its hash differs from initial, or ctx expires.
func WaitForChange(ctx context.Context, sc Screenshotter, rect image.Rectangle, initial uint32, poll time.Duration, newHash Hasher) (uint32, error) {
	for {
		h, err := GrabHash(sc, rect, newHash)
		if err != nil {
			return 0, err
		}
		if h != initial {
			return h, nil
		}
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("find: timeout waiting for change in rect %v (hash stable at %08x)", rect, initial)
		case <-time.After(poll):
		}
	}
}

// ScanFor polls each rect every poll interval until one matches the corresponding want hash, or ctx expires.
func ScanFor(ctx context.Context, sc Screenshotter, rects []image.Rectangle, wants []uint32, poll time.Duration, newHash Hasher) (Result, error) {
	if len(rects) != len(wants) {
		return Result{}, fmt.Errorf("find: ScanFor: len(rects)=%d != len(wants)=%d", len(rects), len(wants))
	}
	for {
		for i, rect := range rects {
			h, err := GrabHash(sc, rect, newHash)
			if err != nil {
				return Result{}, err
			}
			if h == wants[i] {
				return Result{Hash: h, Rect: rect}, nil
			}
		}
		select {
		case <-ctx.Done():
			return Result{}, fmt.Errorf("find: timeout scanning %d regions", len(rects))
		case <-time.After(poll):
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
func LocateExact(sc Screenshotter, searchArea image.Rectangle, reference image.Image) (image.Rectangle, error) {
	src, err := sc.Grab(searchArea)
	if err != nil {
		return image.Rectangle{}, fmt.Errorf("find: locate grab: %w", err)
	}

	sb := src.Bounds()
	rb := reference.Bounds()

	if rb.Dx() > sb.Dx() || rb.Dy() > sb.Dy() {
		return image.Rectangle{}, fmt.Errorf("find: reference image larger than search area")
	}

	for y := sb.Min.Y; y <= sb.Max.Y-rb.Dy(); y++ {
		for x := sb.Min.X; x <= sb.Max.X-rb.Dx(); x++ {
			if matchAt(src, reference, x, y) {
				return image.Rect(x, y, x+rb.Dx(), y+rb.Dy()), nil
			}
		}
	}
	return image.Rectangle{}, fmt.Errorf("find: exact match not found")
}

func matchAt(src, ref image.Image, ox, oy int) bool {
	rb := ref.Bounds()
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
		img, err := sc.Grab(searchArea)
		if err != nil {
			return 0, image.Rectangle{}, fmt.Errorf("find: tolerance grab: %w", err)
		}

		sb := img.Bounds()
		w, h := expectedRect.Dx(), expectedRect.Dy()

		if sub, ok := img.(interface {
			SubImage(r image.Rectangle) image.Image
		}); ok {
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
		} else {
			return 0, image.Rectangle{}, fmt.Errorf("find: grabbed image does not support SubImage")
		}

		select {
		case <-ctx.Done():
			return 0, image.Rectangle{}, fmt.Errorf("find: timeout waiting for tolerance match")
		case <-time.After(poll):
		}
	}
}
