package input

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The connection object and pingpong object must have distinct identities.
// Sending ei_pingpong.done to the connection object would be dispatched as an
// unrelated connection request, not a valid ping response.
func TestEISRejectsPingObjectAliasedToConnection(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	defer client.close()
	const connection = eiServerIDFloor + 21
	handshake := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	if _, err := peer.Write(handshake); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.next(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Write(eiRequest(connection, 3, append(eiU64(connection), eiU32(1)...))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.done:
		if err := client.readError(); err == nil || !strings.Contains(err.Error(), "unsupported connection ping") {
			t.Fatalf("aliased ping was accepted: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("aliased ping did not terminate the stream")
	}
}
