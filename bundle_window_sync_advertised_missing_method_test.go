package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

type advertisedSyncWithoutImplementation struct{ *handleWindowManager }

func (*advertisedSyncWithoutImplementation) SupportedOperations() []string {
	return []string{"sync", "list"}
}

// Advertising an optional synchronization operation does not authorize
// success when the backend fails to implement the corresponding interface.
func TestWindowSyncAdvertisedWithoutImplementationFailsClosed(t *testing.T) {
	manager := &advertisedSyncWithoutImplementation{handleWindowManager: newHandleWindowManager()}
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	if err := session.Windows.Sync(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unimplemented sync was accepted: %v", err)
	}
}

// Window enumeration can finish after the caller loses authority. The final
// post-filter check must discard all observed private window metadata.
func TestWindowListRejectsCancellationAfterFilteringResults(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "synthetic-private", Title: "private"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	parent, cancelTimeout := context.WithTimeout(context.Background(), time.Second)
	defer cancelTimeout()
	base, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
	windows, err := session.Windows.List(ctx, WindowMatch{})
	if len(windows) != 0 || !errors.Is(err, context.Canceled) || ctx.calls != 2 {
		t.Fatalf("canceled post-filter enumeration escaped: windows=%v err=%v checks=%d", windows, err, ctx.calls)
	}
}
