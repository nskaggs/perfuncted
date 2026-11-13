//go:build linux

package input

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// A virtual keyboard sends evdev key states only as 0 (released) or 1
// (pressed). Invalid states and below-base XKB codes cannot be forwarded to
// the compositor, even if subsequent frames would otherwise be valid.
func TestVirtualKeyboardRejectsInvalidKeyWireTransitionsBeforeEmission(t *testing.T) {
	k, wire := newTestKeyboard()
	for _, tc := range []struct {
		name           string
		keycode, state uint32
		want           string
	}{
		{"below XKB base", 7, 1, "below the XKB base"},
		{"invalid pressed state", kcReturn, 2, "invalid key state"},
		{"maximum uint32 state", kcReturn, ^uint32(0), "invalid key state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(wire.msgs)
			err := k.sendKey(context.Background(), tc.keycode, tc.state)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid protocol transition accepted: %v", err)
			}
			if len(wire.msgs) != before {
				t.Fatalf("invalid key transition sent %d events", len(wire.msgs)-before)
			}
		})
	}
	if err := k.sendKey(context.Background(), kcReturn, 1); err != nil {
		t.Fatalf("valid key-down rejected: %v", err)
	}
	if err := k.sendKey(context.Background(), kcReturn, 0); err != nil {
		t.Fatalf("valid key-up rejected: %v", err)
	}
	if len(wire.msgs) != 2 {
		t.Fatalf("expected exactly two valid transitions, got %d", len(wire.msgs))
	}
	for i, msg := range wire.msgs {
		if len(msg) != 20 || wl.Uint32(msg[4:8])&0xffff != 1 || wl.Uint32(msg[12:16]) != kcReturn-8 || wl.Uint32(msg[16:20]) != uint32(1-i) {
			t.Fatalf("invalid transmitted key event %d: %x", i, msg)
		}
	}
}
