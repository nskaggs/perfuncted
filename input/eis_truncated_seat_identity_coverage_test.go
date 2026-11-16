package input

import (
	"strings"
	"testing"
	"time"
)

func TestEISHandshakeRejectsTruncatedSeatIdentity(t *testing.T) {
	server, _, cancel, done := startEISenderWithResult(t)
	defer cancel()
	if _, err := server.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		t.Fatal(err)
	}
	assertEISenderAnnouncesPingPong(t, server)

	const connection = eiServerIDFloor + 0x22
	events := eiRequest(0, 2, append(append(eiU32(7), eiU64(connection)...), eiU32(1)...))
	events = append(events, eiRequest(connection, 1, eiU32(9))...)
	if _, err := server.Write(events); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "truncated uint64 argument") {
			t.Fatalf("truncated seat identity result = sender %v, err %v", result.sender, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("EIS setup did not reject the truncated seat identity")
	}
}
