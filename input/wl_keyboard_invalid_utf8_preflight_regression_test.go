//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// The virtual keyboard may receive literal text directly from TypeLiteral.
// Invalid UTF-8 must not silently become U+FFFD while sending a valid prefix.
func TestVirtualKeyboardRejectsInvalidUTF8BeforeSendingAnyPrefix(t *testing.T) {
	bad := string([]byte{0xff, 0xfe})
	for _, tc := range []struct {
		name    string
		actions []keySend
	}{
		{"literal text at start", []keySend{{text: bad}}},
		{"invalid trailing text", []keySend{{text: "valid"}, {text: bad}}},
		{"invalid nonnamed key", []keySend{{key: bad}}},
		{"invalid key after accelerator", []keySend{{key: "enter"}, {key: bad}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keyboard, wire := newTestKeyboard()
			err := keyboard.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("malformed keyboard text accepted: %v", err)
			}
			if len(wire.msgs) != 0 || len(keyboard.held) != 0 || keyboard.mods != 0 {
				t.Fatalf("invalid input applied partial keyboard state: %d messages, mods=%d held=%v", len(wire.msgs), keyboard.mods, keyboard.held)
			}
		})
	}
}
