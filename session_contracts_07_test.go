package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/window"
	"testing"
	"time"
)

// Window mutations are scoped to a session-backed stable handle. An absent
// owner must reject every operation before attempting desktop input or
// accidentally forwarding an empty native identifier to a compositor.
func TestUnownedWindowMutationEntryPointsRejectBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	operations := []struct {
		name string
		run  func(*Window) error
	}{
		{"activate", func(w *Window) error { return w.Activate(ctx) }},
		{"move", func(w *Window) error { return w.Move(ctx, 10, 20) }},
		{"resize", func(w *Window) error { return w.Resize(ctx, 640, 480) }},
		{"close", func(w *Window) error { return w.Close(ctx) }},
		{"minimize", func(w *Window) error { return w.Minimize(ctx) }},
		{"maximize", func(w *Window) error { return w.Maximize(ctx) }},
		{"fullscreen", func(w *Window) error { return w.Fullscreen(ctx) }},
		{"unfullscreen", func(w *Window) error { return w.Unfullscreen(ctx) }},
		{"restore", func(w *Window) error { return w.Restore(ctx) }},
	}
	for _, owner := range []struct {
		name   string
		handle *Window
	}{
		{name: "nil handle"},
		{name: "unowned handle", handle: &Window{}},
	} {
		t.Run(owner.name, func(t *testing.T) {
			for _, op := range operations {
				t.Run(op.name, func(t *testing.T) {
					if err := op.run(owner.handle); !errors.Is(err, ErrNilSession) {
						t.Fatalf("unowned window operation accepted: %v", err)
					}
				})
			}
		})
	}
}

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
