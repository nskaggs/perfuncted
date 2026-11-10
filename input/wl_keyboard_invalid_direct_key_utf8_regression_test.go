package input

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// Direct KeyDown must never translate malformed UTF-8 into a real replacement
// character press: the caller did not authorize that physical key event.
func TestVirtualKeyboardDirectKeyDownRejectsInvalidUTF8BeforeWire(t *testing.T) {
	for i, raw := range []string{string([]byte{0xff}), string([]byte{'a', 0xff})} {
		t.Run(fmt.Sprintf("invalid-utf8-%d", i), func(t *testing.T) {
			k, wire := newTestKeyboard()
			err := k.pressKey(context.Background(), raw)
			if err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
				t.Fatalf("invalid direct key accepted: %v", err)
			}
			if len(wire.msgs) != 0 || len(k.held) != 0 || k.mods != 0 {
				t.Fatalf("invalid key emitted compositor events: %d held=%v mods=%d", len(wire.msgs), k.held, k.mods)
			}
		})
	}
}
