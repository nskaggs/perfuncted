package input

import (
	"strings"
	"testing"
	"time"
)

// EIS protocol objects have connection-wide identities; the seat cannot be
// the connection itself, or authorization and capability messages are aliased.
func TestEISHandshakeRejectsSeatAliasedToConnection(t *testing.T) {
	server, _, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	events := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	events = append(events, eiRequest(connection, 1, eiU64(connection))...)
	if _, err := server.Write(events); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid seat") {
			t.Fatalf("aliased seat accepted: sender=%v err=%v", result.sender, result.err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("aliased EIS seat not rejected promptly")
	}
}
