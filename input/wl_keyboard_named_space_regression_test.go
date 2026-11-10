package input

import (
	"context"
	"strings"
	"testing"
)

// The public key syntax accepts {space}. It must represent one physical
// Space key, not type the five letters of the key's name.
func TestVirtualKeyboardNamedSpaceEmitsOneKeyTap(t *testing.T) {
	k, wire := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "space"}}); err != nil {
		t.Fatalf("named space tap: %v", err)
	}
	keyMessages := 0
	for _, msg := range wire.msgs {
		if len(msg) >= 8 && uint16(msg[4]) == 1 {
			keyMessages++
		}
	}
	if keyMessages != 2 {
		t.Fatalf("named space emitted %d key transitions, want one down and one up", keyMessages)
	}
}

func TestVirtualKeyboardNamedSpaceHoldAndRelease(t *testing.T) {
	k, _ := newTestKeyboard()
	if err := k.sendkeys(context.Background(), []keySend{{key: "space", down: true}}); err != nil {
		t.Fatalf("hold space: %v", err)
	}
	if len(k.held) != 1 {
		t.Fatalf("space hold was not recorded: %v", k.held)
	}
	if err := k.sendkeys(context.Background(), []keySend{{key: "space", up: true}}); err != nil {
		t.Fatalf("release space: %v", err)
	}
	if len(k.held) != 0 {
		t.Fatalf("space key remains held: %v", k.held)
	}
}

func TestVirtualKeyboardNamedSpaceSharesLiteralHeldOwnership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		held    string
		attempt string
	}{
		{"literal held named tap", " ", "space"},
		{"named held literal tap", "space", " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			if err := k.pressKey(context.Background(), tc.held); err != nil {
				t.Fatalf("hold %q: %v", tc.held, err)
			}
			prior := len(wire.msgs)
			err := k.sendkeys(context.Background(), []keySend{{key: tc.attempt}})
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("tap %q while held %q: %v", tc.attempt, tc.held, err)
			}
			if len(wire.msgs) != prior {
				t.Fatalf("held space tap emitted %d wire messages", len(wire.msgs)-prior)
			}
			if err := k.releaseKey(context.Background(), "space"); err != nil {
				t.Fatalf("release named space: %v", err)
			}
			if len(k.held) != 0 {
				t.Fatalf("held state not cleared: %v", k.held)
			}
		})
	}
}
