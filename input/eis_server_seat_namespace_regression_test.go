package input

import (
	"strings"
	"testing"
	"time"
)

// Server-advertised seats are protocol objects. A seat in the client-owned
// range can collide with the callback object allocated during device setup.
func TestEISHandshakeRejectsServerSeatInClientNamespace(t *testing.T) {
	for _, tc := range []struct {
		name string
		seat uint64
	}{
		{"first client callback", 1},
		{"later client callback", 17},
		{"client namespace boundary", eiServerIDFloor - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, cancel, done := startEISenderWithResult(t)
			defer cancel()
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatal(err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			const connection = eiServerIDFloor + 0x10
			events := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
			events = append(events, eiRequest(connection, 1, eiU64(tc.seat))...)
			if _, err := server.Write(events); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid seat") {
					t.Fatalf("client-owned seat %x accepted: sender=%v err=%v", tc.seat, result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("client-owned seat %x not rejected before capabilities", tc.seat)
			}
		})
	}
}
