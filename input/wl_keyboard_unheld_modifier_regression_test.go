//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

func TestVirtualKeyboardDirectReleaseRejectsUnheldModifiersWithoutWireEvent(t *testing.T) {
	for _, key := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(key, func(t *testing.T) {
			keyboard, wire := newTestKeyboard()
			if err := keyboard.releaseKey(context.Background(), key); err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("release of unheld modifier %q returned %v", key, err)
			}
			if len(wire.msgs) != 0 {
				t.Fatalf("release of unheld modifier emitted %d compositor messages", len(wire.msgs))
			}
			if keyboard.mods != 0 {
				t.Fatalf("unheld modifier changed logical state to %#x", keyboard.mods)
			}
		})
	}
}
