package perfuncted

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"
)

// The screen facade must withhold a matching frame if its predicate revokes
// caller authorization, and it must preserve the predicate's own timeout
// context rather than treating a stale matching frame as success.
func TestScreenWaitForFnRejectsFrameAfterPredicateCancelsCaller(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 3, 2))
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invoked := 0
	img, err := session.Screen.WaitForFn(ctx, frame.Bounds(), func(_ context.Context, _ image.Image) bool {
		invoked++
		cancel()
		return true
	}, time.Millisecond)
	if invoked != 1 || img != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled predicate published image=%T err=%v calls=%d", img, err, invoked)
	}
}

// Malformed caller predicates must not silently report successful screenshots
// or spin until the deadline; the error is a caller input error, not a capture.
func TestScreenWaitForFnRejectsMissingPredicateBeforePolling(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 3, 2))
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	img, err := session.Screen.WaitForFn(context.Background(), frame.Bounds(), nil, time.Millisecond)
	if img != nil || err == nil || !strings.Contains(err.Error(), "nil predicate") {
		t.Fatalf("missing predicate accepted: image=%T err=%v", img, err)
	}
}
