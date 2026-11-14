package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// A failed enumeration cannot be treated as an empty window set. Find and
// Wait must not return a stale handle, and a transient manager read failure
// may be retried only while the caller still authorizes the wait.
func TestWindowDiscoveryFailureDoesNotManufactureWindowOrSuccess(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "stable-1", Title: "private title"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	failure := errors.New("synthetic window discovery connection timeout")
	manager.setError(failure)
	match := WindowMatch{TitleExact: "private title"}
	got, err := session.Windows.Find(context.Background(), match)
	if got != nil || !errors.Is(err, failure) {
		t.Fatalf("failed discovery accepted window=%v err=%v", got, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	got, err = session.Windows.Wait(ctx, match, WaitEvery(time.Millisecond))
	if got != nil || !errors.Is(err, failure) {
		t.Fatalf("unreadable backend yielded window=%v err=%v", got, err)
	}
	manager.setError(nil)
	got, err = session.Windows.Wait(context.Background(), match)
	if err != nil || got == nil || got.ID().String() != "stable-1" {
		t.Fatalf("recovered backend could not discover stable window: window=%v err=%v", got, err)
	}
}
