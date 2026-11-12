package input

import (
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/internal/keymap"
)

// GNOME holds user-owned modifiers across composite keyboard operations. On
// partial failure, cleanup may discard only modifiers actually released;
// neither another held modifier nor a required temporary modifier may vanish.
func TestGnomeModifierCleanupPreservesEveryUnreleasedCombination(t *testing.T) {
	asModifiers := func(bits int) modifiers {
		return modifiers{shift: bits&1 != 0, ctrl: bits&2 != 0, alt: bits&4 != 0, super: bits&8 != 0}
	}
	for heldBits := 0; heldBits < 16; heldBits++ {
		for releasedBits := 0; releasedBits < 16; releasedBits++ {
			held := asModifiers(heldBits)
			clearHeldModifiers(&held, asModifiers(releasedBits))
			want := asModifiers(heldBits &^ releasedBits)
			if held != want {
				t.Fatalf("held=%04b released=%04b => %+v, want %+v", heldBits, releasedBits, held, want)
			}
		}
	}
	clearHeldModifiers(nil, asModifiers(15))
}

// All four GNOME modifiers must resolve to the expected physical keyvals in
// deterministic press order. Temporary key presses must never re-press a key
// the caller explicitly holds, even when multiple modifiers are involved.
func TestGnomeTemporaryModifierKeysExcludeAllCallerHeldKeys(t *testing.T) {
	keys := []uint32{gnomeSpecialKeyvals[keymap.KeyShift], gnomeSpecialKeyvals[keymap.KeyCtrl], gnomeSpecialKeyvals[keymap.KeyAlt], gnomeSpecialKeyvals[keymap.KeySuper]}
	asModifiers := func(bits int) modifiers {
		return modifiers{shift: bits&1 != 0, ctrl: bits&2 != 0, alt: bits&4 != 0, super: bits&8 != 0}
	}
	b := &GnomeNativeBackend{}
	for requested := 0; requested < 16; requested++ {
		want := make([]uint32, 0, 4)
		for bit, key := range keys {
			if requested&(1<<bit) != 0 {
				want = append(want, key)
			}
		}
		if got := gnomeModifierKeyvals(asModifiers(requested)); !reflect.DeepEqual(got, want) {
			t.Fatalf("requested=%04b keys=%#v want=%#v", requested, got, want)
		}
		for held := 0; held < 16; held++ {
			temporary := make([]uint32, 0, 4)
			for bit, key := range keys {
				if requested&(1<<bit) != 0 && held&(1<<bit) == 0 {
					temporary = append(temporary, key)
				}
			}
			if got := b.temporaryModifierKeyvals(asModifiers(requested), asModifiers(held)); !reflect.DeepEqual(got, temporary) {
				t.Fatalf("requested=%04b held=%04b temporary=%#v want=%#v", requested, held, got, temporary)
			}
		}
	}
}
