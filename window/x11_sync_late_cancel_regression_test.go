//go:build linux

package window

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/x11"
)

type cancelDuringX11SyncConnection struct {
	x11.Connection
	cancel context.CancelFunc
	calls  int
}

func (c *cancelDuringX11SyncConnection) Sync() { c.calls++; c.cancel() }

// Sync must not report success when the caller cancels while its blocking
// X11 round trip completes, even if the transport method has no error return.
func TestX11SyncReportsCancellationDuringTransportRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, _ := newStubX11Backend(t, false, "")
	conn := &cancelDuringX11SyncConnection{Connection: b.conn, cancel: cancel}
	b.conn = conn
	err := b.Sync(ctx)
	if !errors.Is(err, context.Canceled) || conn.calls != 1 {
		t.Fatalf("sync acknowledged canceled request: err=%v calls=%d", err, conn.calls)
	}
}
