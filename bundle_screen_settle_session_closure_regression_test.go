package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

type countingSettleCapture struct {
	syntheticMalformedScreenshotter
	grabs int
}

func (s *countingSettleCapture) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs++
	return s.syntheticMalformedScreenshotter.Grab(ctx, rect)
}

// A successful baseline does not authorize polling after an action closes the
// session. The action is an explicit user-controlled boundary in this facade.
func TestScreenSettleStopsWhenActionClosesSession(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	source := &countingSettleCapture{syntheticMalformedScreenshotter: syntheticMalformedScreenshotter{frame: frame}}
	session := NewSessionForTesting(source, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	actionCalls := 0
	hash, err := session.Screen.WaitForSettle(ctx, frame.Bounds(), func() error {
		actionCalls++
		return session.Close()
	}, 1, time.Millisecond)
	if !errors.Is(err, ErrSessionClosed) || hash != 0 || actionCalls != 1 || source.grabs != 1 {
		t.Fatalf("closed session polled again: hash=%x err=%v actions=%d grabs=%d", hash, err, actionCalls, source.grabs)
	}
}
