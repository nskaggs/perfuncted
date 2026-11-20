package find

import (
	"context"
	"errors"
	"image"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Separate-region polling must not continue to the second region when the
// caller revokes authority at the handoff after the first completed hash.
type revokeOnNthScanCheck struct {
	context.Context
	cancel context.CancelFunc
	calls  int
}

func (c *revokeOnNthScanCheck) Err() error {
	pc, _, _, ok := runtime.Caller(2)
	if ok {
		fn := runtime.FuncForPC(pc)
		if fn != nil && strings.HasSuffix(fn.Name(), ".ScanFor") {
			c.calls++
			if c.calls == 2 {
				c.cancel()
			}
		}
	}
	return c.Context.Err()
}

func TestScanForRevocationAfterFirstSeparatedHashDoesNotCaptureNextRegion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checked := &revokeOnNthScanCheck{Context: ctx, cancel: cancel}
	source := &pixelInspectionScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 2, 2))}
	rects := []image.Rectangle{image.Rect(0, 0, 2, 2), image.Rect(100, 100, 102, 102)}
	got, err := ScanFor(checked, source, rects, []uint32{0xdeadbeef, 0x12345678}, time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) || got != (Result{}) || source.grabs != 1 || checked.calls != 2 {
		t.Fatalf("revoked scan continued: got=%+v err=%v grabs=%d checks=%d", got, err, source.grabs, checked.calls)
	}
}
