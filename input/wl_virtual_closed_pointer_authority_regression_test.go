package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A compositor that cannot report pointer location may return Unsupported
// while active, but must report its revoked backend state after Close. In
// particular, closing cannot reauthorize any pointer query through a stale
// virtual-device handle or leak previously observed pointer coordinates.
func TestWlVirtualPointerLocationRejectsQueriesAfterShutdown(t *testing.T) {
	var absent *WlVirtualBackend
	if x, y, err := absent.PointerLocation(context.Background()); x != 0 || y != 0 || !errors.Is(err, ErrNotSupported) {
		t.Fatalf("nil backend pointer result=%d,%d err=%v", x, y, err)
	}
	b, conn := newCapturedWlVirtualBackend(t)
	if x, y, err := b.PointerLocation(context.Background()); x != 0 || y != 0 || !errors.Is(err, ErrNotSupported) {
		t.Fatalf("active unsupported pointer result=%d,%d err=%v", x, y, err)
	}
	if err := b.checkOpen(); err != nil {
		t.Fatalf("live backend was marked closed: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		x, y, err := b.PointerLocation(context.Background())
		if x != 0 || y != 0 || err == nil || !strings.Contains(err.Error(), "backend is closed") || errors.Is(err, ErrNotSupported) {
			t.Fatalf("closed backend returned pointer data/unsupported: %d,%d %v", x, y, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if x, y, err := b.PointerLocation(ctx); x != 0 || y != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled closed query result=%d,%d err=%v", x, y, err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated close: %v", err)
	}
	if messages := readCapturedWlWrites(t, conn); len(messages) != 0 {
		t.Fatalf("pointer queries leaked %d protocol messages", len(messages))
	}
}
