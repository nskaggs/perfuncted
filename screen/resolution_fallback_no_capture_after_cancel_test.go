package screen

import (
	"context"
	"errors"
	"image"
	"testing"
)

// The fallback may be invoked internally after an earlier availability
// decision. Cancellation must still take precedence at the last boundary
// before capturing the entire desktop, including a backend that ignores ctx.
func TestResolutionFallbackDoesNotCaptureAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &syntheticResolutionCapture{grab: func(context.Context, image.Rectangle) (image.Image, error) {
		t.Fatal("canceled fallback performed full-desktop capture")
		return nil, nil
	}}
	width, height, err := resolutionFromGrab(ctx, backend)
	if width != 0 || height != 0 || !errors.Is(err, context.Canceled) || backend.calls != 0 {
		t.Fatalf("canceled resolution fallback returned %dx%d, error=%v, captures=%d", width, height, err, backend.calls)
	}
}
