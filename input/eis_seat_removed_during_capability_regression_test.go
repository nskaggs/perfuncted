package input

import (
	"errors"
	"testing"
	"time"
)

// A seat removed before capability enumeration completes is no longer an
// authorized target. Setup must not wait for a completion message from a
// destroyed seat, or publish a sender based on its stale grant.
func TestEISSeatRemovalDuringCapabilityEnumerationRevokesImmediately(t *testing.T) {
	server, _, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)
	const connection = eiServerIDFloor + 0x10
	const seat = eiServerIDFloor + 0x11
	stream := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	stream = append(stream, eiRequest(connection, 1, eiU64(seat))...)
	stream = append(stream, eiRequest(seat, 2, append(eiU64(1), eiString("ei_keyboard")...))...)
	stream = append(stream, eiRequest(seat, 0, nil)...)
	if _, err := server.Write(stream); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || !errors.Is(result.err, ErrRemoteDesktopRevoked) {
			t.Fatalf("removed seat accepted or misclassified: sender=%v err=%v", result.sender, result.err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("removed seat left EIS setup waiting for stale capabilities")
	}
}
