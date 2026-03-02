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
