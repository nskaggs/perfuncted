package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"
)

// The FirstPixel helper checks caller authority while acquiring and sampling
// the frame. The facade must independently check it once more before it
// releases the sampled private pixel to an application.
func TestScreenGetPixelFinalCallerRevocationDoesNotPublishSample(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, color.RGBA{R: 101, G: 44, B: 9, A: 255})
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	parent, cancelDeadline := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelDeadline()
	owner, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 4}
	pixel, err := session.Screen.GetPixel(ctx, 0, 0)
	if pixel != (color.RGBA{}) || !errors.Is(err, context.Canceled) || ctx.calls != 4 {
		t.Fatalf("final caller revocation leaked pixel=%+v err=%v checks=%d", pixel, err, ctx.calls)
	}
}

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

// The predicate may complete after its session was closed. The desktop
// facade must not expose a captured image from that now-closed session.
func TestScreenWaitForFnDoesNotReturnImageAfterSessionClose(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	invoked := false
	got, err := session.Screen.WaitForFn(context.Background(), img.Bounds(), func(_ context.Context, _ image.Image) bool {
		invoked = true
		if closeErr := session.Close(); closeErr != nil {
			t.Errorf("session close: %v", closeErr)
		}
		return true
	}, time.Millisecond)
	if !invoked || !errors.Is(err, ErrSessionClosed) || got != nil {
		t.Fatalf("wait after session close leaked %T, error=%v predicate invoked=%v", got, err, invoked)
	}
}
