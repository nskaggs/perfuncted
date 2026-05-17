package util

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math/bits"
	"reflect"
	"time"

	"github.com/nskaggs/perfuncted/find"
)

// MatchResult is a thin description of a matched template in an image.
type MatchResult struct {
	Match bool
	Score float64
	Rect  image.Rectangle
}

// WaitForPixelColor polls rect until a pixel within tolerance of target appears,
// or the timeout expires. Tolerance is applied per channel (0..255).
func WaitForPixelColor(sc find.Screenshotter, rect image.Rectangle, target color.RGBA, tolerance int, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := find.WaitForFn(ctx, sc, rect, func(_ context.Context, img image.Image) bool {
		_, ok := find.PixelFound(img, rect, target, tolerance)
		return ok
	}, 200*time.Millisecond)
	if err != nil {
		return false, err
	}
	return true, nil
}

// WaitForImage waits until template is found in the full screen using method.
// Supported methods: "exact" (pixel-perfect). Returns a slice of MatchResult.
func WaitForImage(sc find.Screenshotter, template image.Image, method string, timeout time.Duration) ([]MatchResult, error) {
	if err := checkAvailable(sc); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	searchArea := image.Rectangle{}
	if r, ok := sc.(interface {
		Resolution() (int, int, error)
	}); ok {
		if w, h, err := r.Resolution(); err == nil && w > 0 && h > 0 {
			searchArea = image.Rect(0, 0, w, h)
		}
	}
	if searchArea.Empty() {
		// Probe full-screen bounds by asking for a zero rect grab; many backends
		// return the full-output image for a zero rect.
		img, err := sc.Grab(ctx, image.Rect(0, 0, 0, 0))
		if err != nil {
			return nil, fmt.Errorf("util: probe screen bounds: %w", err)
		}
		searchArea = img.Bounds()
	}
	switch method {
	case "", "exact":
		r, err := find.WaitForLocate(ctx, sc, searchArea, template, 200*time.Millisecond)
		if err != nil {
			return nil, err
		}
		return []MatchResult{{Match: true, Score: 1.0, Rect: r}}, nil
	default:
		return nil, fmt.Errorf("util: unsupported match method %q", method)
	}
}

func checkAvailable(resource any) error {
	if resource == nil {
		return fmt.Errorf("util: resource not available")
	}
	v := reflect.ValueOf(resource)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if v.IsNil() {
			return fmt.Errorf("util: resource not available")
		}
	}
	return nil
}

// ImageHashCompare returns true when the Hamming distance between two 32-bit
// hashes is <= tolerance. Tolerance is interpreted as number of differing bits.
func ImageHashCompare(hash1, hash2 uint32, tolerance int) bool {
	d := hash1 ^ hash2
	return bits.OnesCount32(d) <= tolerance
}
