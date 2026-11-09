package input

import (
	"context"
	"strings"
	"testing"
	"time"
)

// EI assigns object IDs from distinct client and server namespaces. Accepting
// a server connection object with a client-owned ID aliases setup callbacks
// and subsequent connection messages with unrelated client requests.
func TestEISRejectsServerConnectionInClientObjectNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   uint64
	}{
		{name: "first callback ID", id: 1},
		{name: "subsequent client object", id: 17},
		{name: "highest client-owned ID", id: eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := unixConnectionPair(t)
			defer peer.Close()
			client := newEIClient(conn)
			defer client.close()
			handshake := eiRequest(0, 2, append(append(eiU32(9), eiU64(tc.id)...), eiU32(1)...))
			if _, err := peer.Write(handshake); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), "connection") {
					t.Fatalf("invalid connection ID %x not rejected: %v", tc.id, err)
				}
				if got := client.connection.Load(); got != 0 {
					t.Fatalf("invalid connection ID became authoritative: %x", got)
				}
				if msg, err := client.next(ctx); err == nil || msg.object != 0 {
					t.Fatalf("invalid ID delivered to protocol setup: %+v %v", msg, err)
				}
			case <-ctx.Done():
				t.Fatalf("invalid connection ID %x remained accepted", tc.id)
			}
		})
	}
}

func TestEISAcceptsConnectionAtServerObjectNamespaceBoundary(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	defer client.close()
	handshake := eiRequest(0, 2, append(append(eiU32(9), eiU64(eiServerIDFloor)...), eiU32(1)...))
	if _, err := peer.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if msg, err := client.next(ctx); err != nil || msg.object != 0 || msg.opcode != 2 {
		t.Fatalf("valid boundary connection failed: %+v %v", msg, err)
	}
	if got := client.connection.Load(); got != eiServerIDFloor {
		t.Fatalf("boundary connection stored as %x", got)
	}
}
