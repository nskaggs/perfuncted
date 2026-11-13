package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

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
