package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

// The action may withdraw caller authority after the initial hash was read.
// No change-detection or stability polling is allowed after that cancellation.
func TestScreenWaitForSettleCancellingActionSkipsChangePoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &countingSettleCapture{syntheticMalformedScreenshotter: syntheticMalformedScreenshotter{frame: image.NewRGBA(image.Rect(0, 0, 2, 2))}}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	actions := 0
	hash, err := session.Screen.WaitForSettle(ctx, image.Rect(0, 0, 2, 2), func() error {
		actions++
		cancel()
		return nil
	}, 1, time.Millisecond)
	if !errors.Is(err, context.Canceled) || hash != 0 || actions != 1 {
		t.Fatalf("canceled action proceeded: hash=%08x err=%v actions=%d", hash, err, actions)
	}
	if backend.grabs != 1 {
		t.Fatalf("canceled action started new screen polling phase: calls=%d", backend.grabs)
	}
}
