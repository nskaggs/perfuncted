//go:build linux

package input

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A peer can close only its sending direction without closing the Unix socket.
// This still terminates the EI event stream and must retire protocol authority;
// the reader must not wait forever for another event or deliver a zero frame.
func TestEIClientPeerWriteHalfCloseTerminatesReaderAndRejectsEvents(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	if err := serverConn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
	case <-time.After(time.Second):
		t.Fatal("EI reader remained active after peer half-close")
	}
	if err := client.readError(); err == nil || (!errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "peer closed")) {
		t.Fatalf("peer half-close was not terminal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := client.next(ctx)
	if err == nil || event.object != 0 || event.opcode != 0 || len(event.body) != 0 {
		t.Fatalf("half-closed transport delivered an event: %+v %v", event, err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, net.ErrClosed) {
		t.Fatalf("EI reader did not publish the actual half-close failure: %v", err)
	}
}
