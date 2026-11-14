package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

type closingOnChangedFrame struct {
	owner *Session
	calls int
}

func (s *closingOnChangedFrame) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	frame := image.NewRGBA(image.Rect(0, 0, 3, 3))
	if s.calls >= 2 {
		frame.SetRGBA(0, 0, color.RGBA{R: 200, A: 255})
		if s.calls == 2 {
			if err := s.owner.Close(); err != nil {
				return nil, err
			}
		}
	}
	return frame, nil
}
func (s *closingOnChangedFrame) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (s *closingOnChangedFrame) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (s *closingOnChangedFrame) Close() error { return nil }

// A session may close during the first change-detection frame. Once the
// changed hash returns, the facade must recheck ownership before starting the
// distinct settle phase, even if the caller context itself is still live.
func TestScreenSettleChangePhaseDoesNotPollAfterSessionCloses(t *testing.T) {
	source := &closingOnChangedFrame{}
	session := NewSessionForTesting(source, nil, nil, nil, nil)
	source.owner = session
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	hash, err := session.Screen.WaitForSettle(ctx, image.Rect(0, 0, 3, 3), nil, 1, time.Millisecond)
	if !errors.Is(err, ErrSessionClosed) || hash != 0 || source.calls != 2 {
		t.Fatalf("closed session entered second settle phase: hash=%x err=%v capture calls=%d", hash, err, source.calls)
	}
}
