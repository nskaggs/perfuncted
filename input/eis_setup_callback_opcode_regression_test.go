package input

import (
	"strings"
	"testing"
	"time"
)

// The client-owned ei_callback has only a done event. A peer sending another
// opcode to that callback must be rejected rather than silently ignored while
// input setup waits for a completion event that may never come.
func TestEISSetupCallbackRejectsUnexpectedOpcodeOnWire(t *testing.T) {
	for _, opcode := range []uint32{1, 2, 3} {
		t.Run(string(rune('0'+opcode)), func(t *testing.T) {
			server, _, done := prepareEISInvalidGrantEndpoint(t)
			if _, err := server.Write(eiRequest(1, opcode, eiU64(0))); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.sender != nil || result.err == nil || !strings.Contains(result.err.Error(), "invalid device setup callback") {
					t.Fatalf("unexpected callback opcode %d accepted: sender=%v err=%v", opcode, result.sender, result.err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("callback opcode %d ignored without rejecting setup", opcode)
			}
		})
	}
}
