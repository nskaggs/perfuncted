package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

// bundleBase is embedded by value, so the promoted trace method's receiver is
// &s.bundleBase. Calling a traced entry point on a nil bundle dereferenced it to
// compute that receiver and panicked before any nil check could report the
// unavailable session.
func TestNilScreenBundleCaptureReturnsUnavailable(t *testing.T) {
	var s *ScreenBundle
	if _, err := s.Capture(context.Background(), image.Rect(0, 0, 10, 10)); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Capture on a nil bundle = %v, want ErrNilSession", err)
	}
}

// Every traced entry point has to tolerate a nil receiver, because each traces
// before it checks the backend.
func TestNilScreenBundleTracedEntrypointsDoNotPanic(t *testing.T) {
	var s *ScreenBundle
	ctx := context.Background()
	rect := image.Rect(0, 0, 10, 10)

	_, _ = s.Grab(ctx, rect)
	_, _ = s.GetAllPixels(ctx)
	_ = s.CaptureRegion(ctx, rect, "")
	_, _ = s.GetPixel(ctx, 0, 0)
	_, _ = s.GetMultiplePixels(ctx, []image.Point{{}})
	_, _ = s.WaitForFn(ctx, rect, func(context.Context, image.Image) bool { return true }, time.Millisecond)
	_, _ = s.Capture(ctx, rect)
}
