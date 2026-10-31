package input

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The ei_handshake.connection event establishes the authoritative server-side
// ei_connection object exactly once. A second announcement must not replace
// it after negotiation: otherwise an attacker-controlled peer can rebind the
// handler that acknowledges pings and controls subsequent transport state.
func TestEISDuplicateHandshakeConnectionCannotReplaceNegotiatedObject(t *testing.T) {
	for _, tc := range []struct {
		name       string
		replayedID uint64
	}{
		{name: "duplicate same connection", replayedID: eiServerIDFloor + 17},
		{name: "rebind different connection", replayedID: eiServerIDFloor + 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := unixConnectionPair(t)
			defer serverConn.Close()
			client := newEIClient(clientConn)
			defer client.close()
			const authoritativeID = eiServerIDFloor + 17
			first := eiRequest(0, 2, append(append(eiU32(1), eiU64(authoritativeID)...), eiU32(1)...))
			if _, err := serverConn.Write(first); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := client.next(ctx); err != nil {
				t.Fatal(err)
			}
			if got := client.connection.Load(); got != authoritativeID {
				t.Fatalf("initial connection %d, want %d", got, authoritativeID)
			}
			replay := eiRequest(0, 2, append(append(eiU32(2), eiU64(tc.replayedID)...), eiU32(1)...))
			if _, err := serverConn.Write(replay); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), "duplicate") {
					t.Fatalf("duplicate handshake not rejected: %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("duplicate handshake did not terminate EIS stream: %v", ctx.Err())
			}
			if got := client.connection.Load(); got != authoritativeID {
				t.Fatalf("duplicate handshake replaced live authority ID with %d", got)
			}
		})
	}
}
