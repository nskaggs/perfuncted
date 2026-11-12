package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// A stable window handle must not mistake a title change for closure, and a
// canceled waiter must never manufacture a successful disappearance. Later
// verified disappearance of the same native ID must complete a fresh wait.
func TestWindowWaitClosedHonorsStableIdentityAndWaiterCancellation(t *testing.T) {
	var absent *Window
	if err := absent.WaitClosed(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil handle wait=%v", err)
	}
	if err := (&Window{}).WaitClosed(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned handle wait=%v", err)
	}
	manager := newHandleWindowManager(window.Info{NativeID: "native-17", Title: "Original"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	handle := &Window{id: WindowID{session: session, native: "native-17"}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handle.WaitClosed(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled waiter=%v", err)
	}
	manager.setWindows(false, window.Info{NativeID: "native-17", Title: "Renamed"})
	ctx, stop := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer stop()
	if err := handle.WaitClosed(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("renamed live window incorrectly considered closed: %v", err)
	}
	manager.setWindows(true)
	doneCtx, doneCancel := context.WithTimeout(context.Background(), time.Second)
	defer doneCancel()
	if err := handle.WaitClosed(doneCtx); err != nil {
		t.Fatalf("verified native-ID disappearance not accepted: %v", err)
	}
}
