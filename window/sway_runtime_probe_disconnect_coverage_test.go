package window

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// Runtime discovery must reject a Sway socket that accepts a query but closes
// before returning the compositor tree.
func TestSwayRuntimeProbePropagatesDisconnectedTreeResponse(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "sway-ipc.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		msgType, _, readErr := readSwayMessage(conn)
		if readErr == nil && msgType != swayMsgGetTree {
			readErr = errors.New("runtime probe sent a non-tree request")
		}
		_ = conn.Close()
		serverDone <- readErr
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = swayQueryOnceContext(ctx, socket, swayMsgGetTree, "")
	if !errors.Is(err, io.EOF) {
		t.Fatalf("runtime probe error = %v, want disconnected tree response", err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic Sway server: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic Sway server did not receive the tree query: %v", ctx.Err())
	}
}
