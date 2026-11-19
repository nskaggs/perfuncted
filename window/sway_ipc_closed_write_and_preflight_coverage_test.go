package window

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
)

// A transport-level send failure must not be disguised as a successful
// query, and no read should follow a request that was never delivered.
func TestSwayQueryConnPreservesWriteFailureBeforeResponse(t *testing.T) {
	conn := &failingSwayConn{writeErr: io.ErrClosedPipe}
	body, err := swayQueryConn(context.Background(), conn, swayMsgGetTree, "private")
	if !errors.Is(err, io.ErrClosedPipe) || len(body) != 0 {
		t.Fatalf("write failure: body=%q err=%v", body, err)
	}
	_, deadlineCalls, writeCalls, _ := conn.snapshot()
	if deadlineCalls != 1 || writeCalls != 1 {
		t.Fatalf("attempts after failed send: deadlines=%d writes=%d", deadlineCalls, writeCalls)
	}
}

// Cancellation at the context-aware query entrypoint must not touch an IPC
// transport, even when an otherwise usable connection is supplied.
func TestSwayQueryConnContextRejectsPreCanceledRequestWithoutIO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &stubSwayConn{}
	body, err := swayQueryConnContext(ctx, conn, swayMsgGetTree, "private")
	if !errors.Is(err, context.Canceled) || len(body) != 0 {
		t.Fatalf("pre-canceled result: body=%q err=%v", body, err)
	}
	_, deadlineCalls, writeCalls, _ := conn.snapshot()
	if deadlineCalls != 0 || writeCalls != 0 {
		t.Fatalf("canceled request touched transport: deadlines=%d writes=%d", deadlineCalls, writeCalls)
	}
}

// Failed window mutations are failures even before a wire message can be
// sent, including after the compositor session has been closed.
func TestSwayCommandPropagatesClosedManagerFailure(t *testing.T) {
	m := &SwayManager{}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.swayCmd(context.Background(), "[con_id=17] focus"); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed manager accepted focus command: %v", err)
	}
}
