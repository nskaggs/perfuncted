package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

type settleFinalCaptureRevocation struct {
	calls              int
	afterStableCapture func()
}

func (f *settleFinalCaptureRevocation) Grab(_ context.Context, _ image.Rectangle) (image.Image, error) {
	f.calls++
	frame := image.NewRGBA(image.Rect(0, 0, 3, 3))
	if f.calls >= 2 {
		frame.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	}
	if f.calls == 3 && f.afterStableCapture != nil {
		f.afterStableCapture()
	}
	return frame, nil
}
func (*settleFinalCaptureRevocation) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*settleFinalCaptureRevocation) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*settleFinalCaptureRevocation) Close() error { return nil }

// A stable frame is not authoritative when either caller or owning Session
// is revoked before that capture returns; the wait must discard its hash.
func TestScreenWaitForSettleDiscardsFinalStableHashAfterRevocation(t *testing.T) {
	for _, mode := range []string{"caller canceled", "session closed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			frame := &settleFinalCaptureRevocation{}
			session := NewSessionForTesting(frame, nil, nil, nil, nil)
			defer session.Close()
			want := context.Canceled
			if mode == "session closed" {
				want = ErrSessionClosed
				frame.afterStableCapture = func() { _ = session.Close() }
			} else {
				frame.afterStableCapture = cancel
			}
			hash, err := session.Screen.WaitForSettle(ctx, image.Rect(0, 0, 3, 3), nil, 1, time.Millisecond)
			if !errors.Is(err, want) || hash != 0 || frame.calls != 3 {
				t.Fatalf("revoked stable frame acknowledged: hash=%x err=%v calls=%d want=%v", hash, err, frame.calls, want)
			}
		})
	}
}
