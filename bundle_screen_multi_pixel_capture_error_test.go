package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
)

// A failed multi-pixel capture must never expose a partially initialized
// result, irrespective of how many private coordinates were requested.
func TestScreenMultiplePixelsRejectsCaptureTransportFailureWithoutPartialResults(t *testing.T) {
	failure := errors.New("synthetic screenshot transport unavailable")
	backend := &failingSettleBaselineCapture{failure: failure}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	points := []image.Point{{X: 1, Y: 2}, {X: 3, Y: 4}, {X: 5, Y: 6}}
	colors, err := session.Screen.GetMultiplePixels(context.Background(), points)
	if colors != nil || !errors.Is(err, failure) || backend.calls != 1 {
		t.Fatalf("failed multi-pixel capture disclosed data: colors=%v err=%v backend calls=%d", colors, err, backend.calls)
	}
}
