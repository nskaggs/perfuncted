package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/keymap"
	"math"
	"reflect"
	"strings"
	"testing"
)

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

// The parsed Type entrypoint validates keys before calling typeAction, but
// typeAction itself must reject an unrecognized key before touching modifier
// ownership or forwarding any synthetic key event to the desktop bridge.
func TestGnomeTypeActionRejectsUnknownKeyBeforeModifierDispatch(t *testing.T) {
	for _, name := range []string{"not-a-real-key", "", string([]byte{0xff})} {
		t.Run(name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{}
			backend := &GnomeNativeBackend{bridge: bridge}
			held := modifiers{shift: true}
			owned := modifiers{ctrl: true}
			err := backend.typeAction(context.Background(), keySend{key: name, modifiers: modifiers{ctrl: true, alt: true, super: true}}, &held, &owned)
			if err == nil {
				t.Fatalf("unknown key %q accepted", name)
			}
			if len(bridge.events) != 0 {
				t.Fatalf("unrecognized key %q dispatched events: %v", name, bridge.events)
			}
			if held != (modifiers{shift: true}) || owned != (modifiers{ctrl: true}) {
				t.Fatalf("unrecognized key changed modifier ownership: held=%+v owned=%+v", held, owned)
			}
			if name == string([]byte{0xff}) && !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid encoding error not preserved: %v", err)
			}
		})
	}
}

// The preflight boundary is shared by different input entry points. A bad
// release name or a malformed suffix in typed-key syntax must never dispatch
// even an otherwise valid prefix or alter a modifier the caller is holding.
func TestGnomeInvalidKeyReleaseAndTypedSequenceNeverDispatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*GnomeNativeBackend) error
	}{
		{"unknown release key", func(b *GnomeNativeBackend) error { return b.KeyUp(context.Background(), "not-a-real-key") }},
		{"invalid UTF8 release", func(b *GnomeNativeBackend) error { return b.KeyUp(context.Background(), string([]byte{0xff})) }},
		{"unclosed key syntax", func(b *GnomeNativeBackend) error { return b.Type(context.Background(), "prefix{ctrl down") }},
		{"empty key syntax", func(b *GnomeNativeBackend) error { return b.Type(context.Background(), "prefix{}") }},
		{"incomplete chord", func(b *GnomeNativeBackend) error { return b.Type(context.Background(), "prefix{ctrl+}") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{}
			backend := &GnomeNativeBackend{bridge: bridge}
			if err := backend.KeyDown(context.Background(), "shift"); err != nil {
				t.Fatal(err)
			}
			bridge.events = nil
			err := tc.invoke(backend)
			if err == nil {
				t.Fatal("invalid GNOME input accepted")
			}
			if len(bridge.events) != 0 {
				t.Fatalf("invalid input reached desktop: %v (error %v)", bridge.events, err)
			}
			if !backend.held.shift || backend.held.ctrl {
				t.Fatalf("invalid input changed held-key ownership: %+v", backend.held)
			}
			if strings.Contains(tc.name, "UTF8") && !strings.Contains(err.Error(), "UTF-8") {
				t.Fatalf("invalid encoding not explained: %v", err)
			}
		})
	}
}

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

// A bridge can reject either side of a key transition. Bookkeeping of caller-
// held modifiers must follow acknowledged events rather than optimistic state
// changes, so an unsuccessful release does not surrender the caller's hold.
func TestGnomeDirectModifierPressAndReleaseFailuresRetainOwnership(t *testing.T) {
	failure := errors.New("injected GNOME virtual key transport failure")
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, stage := range []string{"press", "release"} {
		t.Run(stage, func(t *testing.T) {
			bridge := &recordingGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			ctx := context.Background()
			if stage == "release" {
				if err := b.KeyDown(ctx, "ctrl"); err != nil {
					t.Fatal(err)
				}
				bridge.events = nil
			}
			bridge.failOn = func(state string, keyval uint32) error {
				if keyval == ctrl && ((stage == "press" && state == "down") || (stage == "release" && state == "up")) {
					return failure
				}
				return nil
			}
			var err error
			if stage == "press" {
				err = b.KeyDown(ctx, "ctrl")
			} else {
				err = b.KeyUp(ctx, "ctrl")
			}
			if !errors.Is(err, failure) {
				t.Fatalf("GNOME failure lost: %v", err)
			}
			if held := b.held.ctrl; held != (stage == "release") {
				t.Fatalf("failed %s changed held modifier ownership: held=%t", stage, held)
			}
			expected := "down"
			if stage == "release" {
				expected = "up"
			}
			if countGnomeKeyEvents(bridge.events, expected, ctrl) != 1 {
				t.Fatalf("failed %s did not reach bridge once: %v", stage, bridge.events)
			}
			bridge.failOn = nil
			if stage == "press" {
				if err := b.KeyDown(ctx, "ctrl"); err != nil || !b.held.ctrl {
					t.Fatalf("retry press did not acquire modifier: %v %+v", err, b.held)
				}
			} else {
				if err := b.KeyUp(ctx, "ctrl"); err != nil || b.held.ctrl {
					t.Fatalf("retry release did not relinquish modifier: %v %+v", err, b.held)
				}
			}
		})
	}
}

// GNOME keysyms use ordinary ASCII values for physical digits and the XKB
// Unicode marker for non-Latin-1 codepoints. A physical key and Unicode
// character must never be silently translated to a different identity.
func TestGnomeNativePhysicalDigitsAndExtendedUnicodeKeysyms(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want uint32
	}{
		{"0", '0'}, {"5", '5'}, {"9", '9'},
		{"Ā", 0x01000100}, {"中", 0x01004e2d}, {"🚀", 0x0101f680},
	} {
		t.Run(tc.key, func(t *testing.T) {
			got, err := gnomeKeyval(tc.key)
			if err != nil || got != tc.want {
				t.Fatalf("gnome keyval %q = %#x %v, want %#x", tc.key, got, err, tc.want)
			}
		})
	}
}

// A chord acquires its temporary modifiers one at a time. Failure to acquire
// a later modifier must release only the modifiers acquired earlier, preserve
// failures during release, and never dispatch the chord's primary key.
func TestGnomeChordSecondModifierFailureRollsBackFirstModifier(t *testing.T) {
	pressErr := errors.New("failed to press second modifier")
	cleanupErr := errors.New("failed to release first modifier")
	shift := gnomeSpecialKeyvals[keymap.KeyShift]
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, failCleanup := range []bool{false, true} {
		name := "rollback succeeds"
		if failCleanup {
			name = "rollback also fails"
		}
		t.Run(name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{}
			bridge.failOn = func(state string, keyval uint32) error {
				if state == "down" && keyval == ctrl {
					return pressErr
				}
				if failCleanup && state == "up" && keyval == shift {
					return cleanupErr
				}
				return nil
			}
			backend := &GnomeNativeBackend{bridge: bridge}
			err := backend.Type(context.Background(), "{shift+ctrl+a}")
			if !errors.Is(err, pressErr) || errors.Is(err, cleanupErr) != failCleanup {
				t.Fatalf("partial chord errors lost: %v", err)
			}
			if countGnomeKeyEvents(bridge.events, "down", shift) != 1 || countGnomeKeyEvents(bridge.events, "up", shift) != 1 || countGnomeKeyEvents(bridge.events, "down", ctrl) != 1 || countGnomeKeyEvents(bridge.events, "down", uint32('a')) != 0 {
				t.Fatalf("partial chord crossed failed modifier or lost rollback: %v", bridge.events)
			}
			if backend.held.any() {
				t.Fatalf("partial chord changed caller-held modifier state: %+v", backend.held)
			}
			bridge.events = nil
			bridge.failOn = nil
			if err := backend.Type(context.Background(), "{shift+ctrl+a}"); err != nil {
				t.Fatalf("next independent chord rejected: %v", err)
			}
			if countGnomeKeyEvents(bridge.events, "down", shift) != 1 || countGnomeKeyEvents(bridge.events, "down", ctrl) != 1 {
				t.Fatalf("retry did not reacquire both modifiers: %v", bridge.events)
			}
		})
	}
}

// Invalid releases and coordinates must never reach GNOME's virtual device:
// truncation of an int into an int32 can point at a different application.
func TestGnomePointerRejectsInvalidReleasesAndUnrepresentableClicks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		call    func(*GnomeNativeBackend) error
		explain string
	}{
		{"release button zero", func(b *GnomeNativeBackend) error { return b.MouseUp(context.Background(), 0) }, "button"},
		{"release button four", func(b *GnomeNativeBackend) error { return b.MouseUp(context.Background(), 4) }, "button"},
		{"click x positive overflow", func(b *GnomeNativeBackend) error { return b.MouseClick(context.Background(), math.MaxInt, 9, 1) }, "coordinate"},
		{"click y negative overflow", func(b *GnomeNativeBackend) error { return b.MouseClick(context.Background(), 5, math.MinInt, 3) }, "coordinate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bridge := &pointerGnomeBridge{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := tc.call(b)
			if err == nil || !strings.Contains(err.Error(), tc.explain) {
				t.Fatalf("invalid pointer request admitted: %v", err)
			}
			if len(bridge.events) != 0 {
				t.Fatalf("invalid pointer request leaked compositor events: %v", bridge.events)
			}
		})
	}
}
