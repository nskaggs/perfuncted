package input

import (
	"context"
	"errors"
	"testing"
)

// Socket disconnection must be terminal even if protocol messages were already
// buffered. Replayed EIS events from a dead authorization session are not
// authoritative grants for ongoing device negotiation.
func TestEIClientRefusesBufferedEventsAfterTransportTerminalFailure(t *testing.T) {
	failure := errors.New("synthetic EIS peer disconnected")
	for i := 0; i < 128; i++ {
		c := &eiClient{messages: make(chan eiMessage, 1), done: make(chan struct{})}
		c.messages <- eiMessage{object: 0, opcode: 2, body: append(append(eiU32(1), eiU64(0xff00000000000010)...), eiU32(1)...)}
		c.setReadError(failure)
		close(c.done)
		event, err := c.next(context.Background())
		if !errors.Is(err, failure) {
			t.Fatalf("iteration %d accepted stale EIS event %+v instead of terminal failure: %v", i, event, err)
		}
	}
}
