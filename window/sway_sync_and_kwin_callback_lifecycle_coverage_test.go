package window

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// A live Sway manager can acknowledge its local sync gate, while a retired
// manager cannot claim a usable input/window IPC session.
func TestSwaySyncDistinguishesLiveClosedAndNilManager(t *testing.T) {
	var live SwayManager
	if err := live.Sync(context.Background()); err != nil {
		t.Fatalf("live sync failed: %v", err)
	}
	live.closed.Store(true)
	if err := live.Sync(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed Sway manager acknowledged sync: %v", err)
	}
	var missing *SwayManager
	if err := missing.Sync(context.Background()); err == nil || !strings.Contains(err.Error(), "manager is nil") {
		t.Fatalf("missing Sway manager acknowledged sync: %v", err)
	}
}

// A completed KWin callback must provide the exact compositor payload, not
// substitute an empty or stale result.
func TestKWinCallbackReturnsVerifiedCompositorPayload(t *testing.T) {
	receiver := &pfReceiver{ch: make(chan string, 1)}
	const payload = `[{"id":"34","title":"Editor"}]`
	receiver.ch <- payload
	result, err := waitForKWinResult(context.Background(), receiver, 34)
	if err != nil || result != payload {
		t.Fatalf("valid callback result=%q err=%v", result, err)
	}
}
