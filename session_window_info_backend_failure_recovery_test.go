package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/window"
)

// A failed stable-ID metadata read must not corrupt the cached window handle.
// Once the manager recovers, the same handle can accept a fresh observation.
func TestWindowInfoTransportFailurePreservesCacheAndLaterRecovers(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "stable-id", Title: "original"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := session.Windows.window(window.Info{NativeID: "stable-id", Title: "original"})
	readErr := errors.New("synthetic window transport failure")
	manager.setError(readErr)
	info, err := handle.Info(context.Background())
	if !errors.Is(err, readErr) || info != (window.Info{}) {
		t.Fatalf("failed read leaked info=%+v err=%v", info, err)
	}
	if got := handle.Title(); got != "original" {
		t.Fatalf("failed read changed cached title to %q", got)
	}
	manager.setError(nil)
	manager.setWindows(false, window.Info{NativeID: "stable-id", Title: "updated"})
	info, err = handle.Info(context.Background())
	if err != nil || info.Title != "updated" || handle.Title() != "updated" {
		t.Fatalf("recovered window metadata not accepted: info=%+v err=%v cached=%q", info, err, handle.Title())
	}
}
