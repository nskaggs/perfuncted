package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Loss of a window backend does not prove an ID has disappeared. A closure
// waiter cannot turn a missing observation capability into a success result.
func TestWindowClosedDoesNotInferDisappearanceFromMissingManager(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := &Window{id: WindowID{session: session, native: "unobserved-window"}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ok, err := WindowClosed(handle).evaluate(ctx, session)
	if ok || !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unavailable manager proved closure: ok=%v err=%v", ok, err)
	}
	evidence, err := session.WaitWithEvidence(ctx, WindowClosed(handle), WaitEvery(100*time.Millisecond))
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrUnsupported) || evidence.Evaluations != 1 || evidence.PollWakeups != 0 {
		t.Fatalf("unavailable manager was treated as closed or polled: evidence=%+v err=%v", evidence, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unavailable manager exhausted caller timeout: %v", err)
	}
}
