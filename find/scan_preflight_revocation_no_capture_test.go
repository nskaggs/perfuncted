package find

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

// ScanFor validates input before it enters the polling loop. A caller that
// revokes authority exactly at the first observation check must prevent even
// the first grab in both compact and spatially separated region modes.
type cancelOnFirstScanCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelOnFirstScanCheck) Err() error {
	c.checks++
	if c.checks == 1 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestScanForRejectsRevocationBeforeFirstCapture(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rects []image.Rectangle
	}{
		{name: "compact regions", rects: []image.Rectangle{image.Rect(0, 0, 2, 2), image.Rect(2, 0, 4, 2)}},
		{name: "separated regions", rects: []image.Rectangle{image.Rect(0, 0, 2, 2), image.Rect(100, 100, 102, 102)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checked := &cancelOnFirstScanCheck{Context: ctx, cancel: cancel}
			source := &pixelInspectionScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 4, 4))}
			wants := []uint32{0x1234, 0x5678}
			result, err := ScanFor(checked, source, tc.rects, wants, time.Millisecond, nil)
			if !errors.Is(err, context.Canceled) || result != (Result{}) || source.grabs != 0 || checked.checks != 1 {
				t.Fatalf("canceled scan captured frame: result=%+v err=%v grabs=%d checks=%d", result, err, source.grabs, checked.checks)
			}
		})
	}
}
