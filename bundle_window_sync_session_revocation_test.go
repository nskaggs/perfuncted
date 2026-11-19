package perfuncted

import (
	"context"
	"errors"
	"testing"
)

type closeDuringManagedWindowSync struct {
	*handleWindowManager
	session *Session
	calls   int
}

func (b *closeDuringManagedWindowSync) Sync(context.Context) error {
	b.calls++
	return b.session.Close() //nolint:contextcheck // Session.Close owns an independent teardown deadline after the backend finishes.
}

// A backend can finish syncing after Session.Close revokes ownership, even
// though the caller's context is still valid. In that case no stale success
// may be returned to the caller.
func TestManagedWindowSyncRejectsBackendCompletionAfterSessionClose(t *testing.T) {
	backend := &closeDuringManagedWindowSync{handleWindowManager: newHandleWindowManager()}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	backend.session = session
	defer func() { _ = session.Close() }()
	err := session.Windows.Sync(context.Background())
	if !errors.Is(err, ErrSessionClosed) || backend.calls != 1 {
		t.Fatalf("sync completed after session closure: err=%v calls=%d", err, backend.calls)
	}
}

// An absent session must not fabricate invalidation notifications or panic
// during cancellation and shutdown of its optional components.
func TestMissingSessionWaitNotificationIsInert(t *testing.T) {
	var missing *Session
	missing.notifyWaiters()
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.notifyWaiters() // No wait hub exists until an observer subscribes.
}
