package input

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A server-created pingpong object cannot borrow a client-owned object ID.
// A reply on such an ID would send a request to a different protocol object.
func TestEISRejectsServerPingInClientObjectNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   uint64
	}{
		{"first callback", 1},
		{"later callback", 17},
		{"namespace boundary", eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := unixConnectionPair(t)
			defer peer.Close()
			client := newEIClient(conn)
			defer client.close()
			const connection = eiServerIDFloor + 21
			if _, err := peer.Write(eiRequest(0, 2, append(append(eiU32(3), eiU64(connection)...), eiU32(1)...))); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := client.next(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := peer.Write(eiRequest(connection, 3, append(eiU64(tc.id), eiU32(1)...))); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), "unsupported connection ping") {
					t.Fatalf("client-owned ping %x accepted: %v", tc.id, err)
				}
			case <-ctx.Done():
				t.Fatalf("client-owned ping %x left transport active", tc.id)
			}
		})
	}
}

func TestEISAcceptsServerPingAtNamespaceBoundary(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	defer client.close()
	const connection = eiServerIDFloor + 21
	if _, err := peer.Write(eiRequest(0, 2, append(append(eiU32(3), eiU64(connection)...), eiU32(1)...))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Write(eiRequest(connection, 3, append(eiU64(eiServerIDFloor), eiU32(1)...))); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 24)
	total := 0
	for total < len(reply) {
		n, err := peer.Read(reply[total:])
		if err != nil {
			t.Fatalf("valid server-owned ping not answered: %v", err)
		}
		total += n
	}
	expected := eiRequest(eiServerIDFloor, 0, eiU64(0))
	if string(reply) != string(expected) {
		t.Fatalf("invalid server-owned pong %x", reply)
	}
}
