//go:build linux

package input

import (
	"testing"

	"github.com/bendahl/uinput"
)

// Only real modifier keycodes may change the modifier ownership bitmask.
// In particular, ordinary keys must not clear a modifier held by the caller.
func TestUinputModifierBookkeepingIgnoresOrdinaryKeysAndKeepsSidesIndependent(t *testing.T) {
	backend := &UinputBackend{}
	backend.markModifierHeld(uinput.KeyLeftshift)
	backend.markModifierHeld(uinput.KeyRightshift)
	backend.markModifierHeld(uinput.KeyLeftctrl)
	initial := backend.heldMods
	if initial == 0 {
		t.Fatal("modifier holds not registered")
	}
	for _, code := range []int{uinput.KeyA, uinput.KeySpace, -1, 0} {
		if backend.modifierHeld(code) {
			t.Fatalf("ordinary keycode %d reported as a held modifier", code)
		}
		backend.markModifierHeld(code)
		if backend.heldMods != initial {
			t.Fatalf("ordinary keycode %d changed modifier ownership: got %b want %b", code, backend.heldMods, initial)
		}
		backend.markModifierReleased(code)
		if backend.heldMods != initial {
			t.Fatalf("ordinary keycode %d released a caller-owned modifier", code)
		}
	}
	backend.markModifierReleased(uinput.KeyLeftshift)
	if backend.modifierHeld(uinput.KeyLeftshift) || !backend.modifierHeld(uinput.KeyRightshift) || !backend.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("releasing left shift changed unrelated modifier ownership")
	}
	backend.markModifierReleased(uinput.KeyRightshift)
	backend.markModifierReleased(uinput.KeyLeftctrl)
	if backend.heldMods != 0 {
		t.Fatalf("released modifiers still held: %b", backend.heldMods)
	}
}

// Close on an absent backend is safe during partially failed initialization.
func TestNilUinputBackendCloseIsSafe(t *testing.T) {
	var backend *UinputBackend
	if err := backend.Close(); err != nil {
		t.Fatalf("nil backend Close: %v", err)
	}
}
