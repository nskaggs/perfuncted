package input

import (
	"context"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// Four-level keyboard maps place Shift+AltGr characters in the fourth
// slot. The backend must apply both modifiers and restore only its own holds.
func TestXTestFourthLevelAppliesShiftAndAltGrInOrder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		holdShift bool
	}{
		{"temporary modifiers", false},
		{"caller-owned shift", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, events := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
			shift, err := backend.keycodeFor("shift")
			if err != nil {
				t.Fatal(err)
			}
			altGr, err := backend.altGrKeycode()
			if err != nil {
				t.Fatal(err)
			}
			key, level, err := backend.keycodeAndLevel('@')
			if err != nil || level != 3 {
				t.Fatalf("at-sign mapped to key=%d level=%d err=%v, want level 3", key, level, err)
			}
			if tc.holdShift {
				if err := backend.KeyDown(context.Background(), "shift"); err != nil {
					t.Fatal(err)
				}
			}
			initial := len(*events)
			if err := backend.TypeLiteral(context.Background(), "@"); err != nil {
				t.Fatalf("fourth-level literal: %v", err)
			}
			var want []xtestEvent
			if !tc.holdShift {
				want = append(want, xtestEvent{eventType: xproto.KeyPress, detail: byte(shift)})
			}
			want = append(want,
				xtestEvent{eventType: xproto.KeyPress, detail: byte(altGr)},
				xtestEvent{eventType: xproto.KeyPress, detail: byte(key)},
				xtestEvent{eventType: xproto.KeyRelease, detail: byte(key)},
				xtestEvent{eventType: xproto.KeyRelease, detail: byte(altGr)},
			)
			if !tc.holdShift {
				want = append(want, xtestEvent{eventType: xproto.KeyRelease, detail: byte(shift)})
			}
			got := (*events)[initial:]
			if len(got) != len(want) {
				t.Fatalf("fourth-level events=%v want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("event %d = %+v want %+v (all %v)", i, got[i], want[i], got)
				}
			}
			if !tc.holdShift && backend.modifierHeld(shift) {
				t.Fatal("temporary shift remains held")
			}
			if tc.holdShift && !backend.modifierHeld(shift) {
				t.Fatal("caller shift was released")
			}
			if backend.modifierHeld(altGr) {
				t.Fatal("temporary AltGr remains held")
			}
		})
	}
}
