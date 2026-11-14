package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

type failingSettleBaselineCapture struct {
	failure error
	calls   int
}

func (s *failingSettleBaselineCapture) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	return nil, s.failure
}
func (*failingSettleBaselineCapture) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*failingSettleBaselineCapture) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*failingSettleBaselineCapture) Close() error { return nil }

// A failed initial frame is not authorization to act. Settle must return the
// original transport error without invoking the action or later polling.
func TestScreenWaitForSettleRejectsInitialCaptureTransportFailure(t *testing.T) {
	failure := errors.New("synthetic baseline capture transport failure")
	source := &failingSettleBaselineCapture{failure: failure}
	session := NewSessionForTesting(source, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	actions := 0
	hash, err := session.Screen.WaitForSettle(context.Background(), image.Rect(0, 0, 3, 2), func() error { actions++; return nil }, 1, time.Millisecond)
	if hash != 0 || !errors.Is(err, failure) || actions != 0 || source.calls != 1 {
		t.Fatalf("failed baseline permitted action: hash=%08x err=%v actions=%d captures=%d", hash, err, actions, source.calls)
	}
}
