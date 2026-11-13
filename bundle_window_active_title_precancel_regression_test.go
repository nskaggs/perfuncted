package perfuncted

import (
	"context"
	"errors"
	"testing"
)

type countingManagedWindowTitleBackend struct {
	*handleWindowManager
	calls int
}

func (b *countingManagedWindowTitleBackend) ActiveTitle(context.Context) (string, error) {
	b.calls++
	return "private title", nil
}

// A pre-canceled request must not trigger a window manager read: even a
// discarded reply can incur a compositor operation after authority expired.
func TestWindowBundleActiveTitlePreCanceledRequestDoesNotReadBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &countingManagedWindowTitleBackend{handleWindowManager: newHandleWindowManager()}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	title, err := session.Windows.ActiveTitle(ctx)
	if !errors.Is(err, context.Canceled) || title != "" || backend.calls != 0 {
		t.Fatalf("canceled title read: title=%q err=%v backend calls=%d", title, err, backend.calls)
	}
}
