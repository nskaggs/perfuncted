package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// Without PID attribution, two matching windows are not proof that either
// belongs to the application. A transport failure likewise cannot authorize
// a window handle; both cases may recover when observation becomes precise.
func TestApplicationWindowWaitDoesNotAcceptAmbiguousOrUnreadableCandidates(t *testing.T) {
	manager := newHandleWindowManager(
		window.Info{NativeID: "first", Title: "Browser"},
		window.Info{NativeID: "second", Title: "Browser"},
	)
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	app := &Application{session: session, done: make(chan struct{})}
	match := WindowMatch{TitleExact: "Browser"}
	for _, tc := range []struct {
		name    string
		failure error
	}{
		{name: "ambiguous candidates"},
		{name: "unreadable candidates", failure: errors.New("synthetic desktop backend offline")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager.setError(tc.failure)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			found, err := app.WaitForWindow(ctx, match)
			if found != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("ambiguous or unreadable candidates accepted: window=%v err=%v", found, err)
			}
		})
	}
	manager.setError(nil)
	manager.setWindows(false, window.Info{NativeID: "first", Title: "Browser"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	found, err := app.WaitForWindow(ctx, match)
	if err != nil || found == nil || found.ID().String() != "first" {
		t.Fatalf("unique candidate not accepted after recovery: window=%v err=%v", found, err)
	}
}
