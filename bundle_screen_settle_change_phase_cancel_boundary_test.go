package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

type cancelOnChangedFrame struct {
	cancel context.CancelFunc
	calls  int
}

func (s *cancelOnChangedFrame) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	frame := image.NewRGBA(image.Rect(0, 0, 3, 3))
	if s.calls >= 2 {
		frame.SetRGBA(0, 0, color.RGBA{R: 200, A: 255})
		if s.calls == 2 {
			s.cancel()
		}
	}
	return frame, nil
}
func (s *cancelOnChangedFrame) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (s *cancelOnChangedFrame) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (s *cancelOnChangedFrame) Close() error { return nil }

// A backend may observe a changed frame and cancel its caller before returning.
// Change detection must not authorize the next polling phase after cancellation,
// even when the changed hash itself was computed successfully.
func TestScreenSettleChangePhaseStopsOnCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancelOnChangedFrame{cancel: cancel}
	session := NewSessionForTesting(source, nil, nil, nil, nil)
	defer session.Close()
	hash, err := session.Screen.WaitForSettle(ctx, image.Rect(0, 0, 3, 3), nil, 1, time.Millisecond)
	if !errors.Is(err, context.Canceled) || hash != 0 || source.calls != 2 {
		t.Fatalf("canceled settle invoked additional captures: hash=%x err=%v calls=%d", hash, err, source.calls)
	}
}
