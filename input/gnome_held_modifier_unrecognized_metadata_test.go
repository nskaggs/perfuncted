package input

import "testing"

// Held modifiers may outlive a failed command or a malformed key identity.
// Neither an absent state object nor an unrecognized key name may create
// phantom modifier state or clear an already-held key.
func TestGnomeHeldModifierStateIgnoresMissingAndUnknownKeyMetadata(t *testing.T) {
	for _, key := range []string{"ctrl", "shift_l", "alt_l", "super", "nonexistent modifier", "", "💡"} {
		updateHeldModifier(nil, key, true)
		updateHeldModifier(nil, key, false)
	}
	held := modifiers{ctrl: true, shift: true, alt: true, super: true}
	for _, key := range []string{"nonexistent modifier", "", "💡", "ctrlish", "f17"} {
		before := held
		updateHeldModifier(&held, key, false)
		if held != before {
			t.Fatalf("unknown key %q released an existing modifier: before=%+v after=%+v", key, before, held)
		}
		updateHeldModifier(&held, key, true)
		if held != before {
			t.Fatalf("unknown key %q pressed a phantom modifier: before=%+v after=%+v", key, before, held)
		}
		if bits := gnomeModifierBits(key); len(bits) != 0 {
			t.Fatalf("unknown modifier key %q resolved to unexpected bits: %v", key, bits)
		}
	}
}
