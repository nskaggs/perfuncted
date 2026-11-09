package input

import (
	"strings"
	"testing"
	"time"
)

// ei_seat.done carries no payload. A server cannot smuggle additional data
// into the capability-completion event and still authorize device binding.
func TestEISRejectsExtendedSeatCapabilityCompletionOverWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"extra word", eiU32(0)},
		{"extra uint64", eiU64(1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _, cancel, done := startEISenderWithResult(t)
			defer cancel()
			if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
				t.Fatal(err)
			}
			assertEISenderAnnouncesPingPong(t, server)
			const conn = eiServerIDFloor + 0x10
			const seat = eiServerIDFloor + 0x11
			stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(conn)...), eiU32(1)...))
			stream = append(stream, eiRequest(conn, 1, eiU64(seat))...)
			for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard"} {
				stream = append(stream, eiRequest(seat, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
			}
			stream = append(stream, eiRequest(seat, 3, tc.body)...)
			if _, err := server.Write(stream); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid seat capability completion") {
					t.Fatalf("extended seat done accepted: sender=%v err=%v", result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("extended seat.done advanced setup instead of failing closed")
			}
		})
	}
}
