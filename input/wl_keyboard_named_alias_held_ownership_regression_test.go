package input

import (
	"context"
	"strings"
	"testing"
)

// Named aliases such as enter/return refer to the same physical key. A tap
// or second hold via an alias must not release or duplicate a caller's hold.
func TestVirtualKeyboardNamedAliasesCannotBypassHeldOwnership(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []keySend
	}{
		{"tap alias", []keySend{{key: "return"}}},
		{"hold alias", []keySend{{key: "return", down: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, wire := newTestKeyboard()
			k.held = map[string]uint32{"enter": kcReturn}
			err := k.sendkeys(context.Background(), tc.actions)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("alias crossed held-key ownership: err=%v", err)
			}
			if len(wire.msgs) != 0 {
				t.Fatalf("alias emitted %d compositor messages", len(wire.msgs))
			}
			if len(k.held) != 1 || k.held["enter"] != kcReturn {
				t.Fatalf("existing hold was changed: %v", k.held)
			}
		})
	}
}

// Releasing a held key through its alias is valid; the original hold must
// be removed rather than treated as an unrelated key.
func TestVirtualKeyboardAliasReleaseMatchesOriginalHold(t *testing.T) {
	k, wire := newTestKeyboard()
	k.held = map[string]uint32{"enter": kcReturn}
	if err := k.sendkeys(context.Background(), []keySend{{key: "return", up: true}}); err != nil {
		t.Fatalf("alias release failed: %v", err)
	}
	if len(k.held) != 0 {
		t.Fatalf("alias release left key depressed: %v", k.held)
	}
	if len(wire.msgs) == 0 {
		t.Fatal("alias release emitted no compositor events")
	}
}
