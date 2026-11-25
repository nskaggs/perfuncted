package input

import (
	"context"
	"errors"
	"fmt"
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

// An explicit release in a typed sequence relinquishes the held modifier.
// When a later text operation fails, cleanup must not release that modifier
// again or retain phantom ownership. The same rule applies to a modifier
// previously held by an independent KeyDown call.
func TestGnomeTypeExplicitModifierReleaseDoesNotDoubleReleaseOnLaterFailure(t *testing.T) {
	failure := errors.New("GNOME text transport failed after modifier release")
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, preheld := range []bool{false, true} {
		name := "acquired within call"
		if preheld {
			name = "held before call"
		}
		t.Run(name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{textErr: failure}
			backend := &GnomeNativeBackend{bridge: bridge}
			ctx := context.Background()
			seq := "{ctrl down}{ctrl up}plain"
			if preheld {
				if err := backend.KeyDown(ctx, "ctrl"); err != nil {
					t.Fatal(err)
				}
				bridge.events = nil
				seq = "{ctrl up}plain"
			}
			err := backend.Type(ctx, seq)
			if !errors.Is(err, failure) {
				t.Fatalf("late text failure lost: %v", err)
			}
			if backend.held.any() {
				t.Fatalf("explicit release retained modifier ownership: %+v", backend.held)
			}
			if got := countGnomeKeyEvents(bridge.events, "up", ctrl); got != 1 {
				t.Fatalf("explicit modifier released %d times, want once: %v", got, bridge.events)
			}
			if got := countGnomeKeyEvents(bridge.events, "down", ctrl); got != map[bool]int{false: 1, true: 0}[preheld] {
				t.Fatalf("unexpected modifier press count: %v", bridge.events)
			}
			bridge.events = nil
			bridge.textErr = nil
			if err := backend.Type(ctx, "{ctrl+a}"); err != nil {
				t.Fatalf("next independent shortcut failed: %v", err)
			}
			if countGnomeKeyEvents(bridge.events, "down", ctrl) != 1 || countGnomeKeyEvents(bridge.events, "up", ctrl) != 1 {
				t.Fatalf("released modifier not reacquired by next shortcut: %v", bridge.events)
			}
		})
	}
}

// A typed sequence that raises a modifier before a later failing operation
// owns that modifier. Its failure cleanup must release it, even when the
// bridge reports a second failure while releasing, and must not leave a
// phantom hold that would affect a subsequent independent operation.
func TestGnomeTypeReleasesNewlyHeldModifierAfterLaterTextFailure(t *testing.T) {
	textFailure := errors.New("GNOME text command failed")
	cleanupFailure := errors.New("GNOME modifier cleanup failed")
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, failCleanup := range []bool{false, true} {
		name := "cleanup succeeds"
		if failCleanup {
			name = "cleanup also fails"
		}
		t.Run(name, func(t *testing.T) {
			bridge := &recordingGnomeBridge{textErr: textFailure}
			if failCleanup {
				bridge.failOn = func(state string, keyval uint32) error {
					if state == "up" && keyval == ctrl {
						return cleanupFailure
					}
					return nil
				}
			}
			backend := &GnomeNativeBackend{bridge: bridge}
			err := backend.Type(context.Background(), "{ctrl down}plain")
			if !errors.Is(err, textFailure) {
				t.Fatalf("lost original text failure: %v", err)
			}
			if failCleanup != errors.Is(err, cleanupFailure) {
				t.Fatalf("cleanup failure was incorrectly represented: %v", err)
			}
			if got := countGnomeKeyEvents(bridge.events, "down", ctrl); got != 1 {
				t.Fatalf("modifier down count %d, events=%v", got, bridge.events)
			}
			if got := countGnomeKeyEvents(bridge.events, "up", ctrl); got != 1 {
				t.Fatalf("modifier cleanup count %d, events=%v", got, bridge.events)
			}
			if backend.held.any() {
				t.Fatalf("failed Type retained phantom modifier: %+v", backend.held)
			}
			bridge.failOn = nil
			bridge.textErr = nil
			bridge.events = nil
			if err := backend.Type(context.Background(), "{ctrl+a}"); err != nil {
				t.Fatalf("independent next shortcut failed: %v", err)
			}
			if got := countGnomeKeyEvents(bridge.events, "down", ctrl); got != 1 {
				t.Fatalf("next shortcut failed to acquire its own modifier: %v", bridge.events)
			}
			if got := countGnomeKeyEvents(bridge.events, "up", ctrl); got != 1 {
				t.Fatalf("next shortcut failed to release its own modifier: %v", bridge.events)
			}
		})
	}
}

// recordingGnomeBridge captures the key events the GNOME backend sends.
type recordingGnomeBridge struct {
	events []string
	// failOn makes a specific key event fail, so a failure can be aimed at the
	// combination's own key rather than at setup.
	failOn func(state string, keyval uint32) error
	// textErr fails literal text, which is the step after a modifier is pressed.
	textErr error
}

func (b *recordingGnomeBridge) Key(_ context.Context, keyval uint32, pressed bool) error {
	state := "down"
	if !pressed {
		state = "up"
	}
	b.events = append(b.events, fmt.Sprintf("%s:%d", state, keyval))
	if b.failOn != nil {
		return b.failOn(state, keyval)
	}
	return nil
}

func (b *recordingGnomeBridge) Text(context.Context, string) error  { return b.textErr }
func (b *recordingGnomeBridge) Paste(context.Context, string) error { return nil }
func (b *recordingGnomeBridge) PointerMove(context.Context, int32, int32) error {
	return nil
}
func (b *recordingGnomeBridge) PointerButton(context.Context, uint32, bool) error { return nil }
func (b *recordingGnomeBridge) PointerLocation(context.Context) (int, int, error) {
	return 0, 0, nil
}
func (b *recordingGnomeBridge) Scroll(context.Context, string, float64) error { return nil }
func (b *recordingGnomeBridge) Close() error                                  { return nil }

var _ gnomeBridge = (*recordingGnomeBridge)(nil)

func countGnomeKeyEvents(events []string, state string, keyval uint32) int {
	want := fmt.Sprintf("%s:%d", state, keyval)
	n := 0
	for _, e := range events {
		if e == want {
			n++
		}
	}
	return n
}

// A modifier the caller is holding through KeyDown must not be pressed or
// released again by a combination that also needs it.
func TestGnomeCombinationPreservesCallerHeldModifier(t *testing.T) {
	bridge := &recordingGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	ctx := context.Background()
	ctrlKey := gnomeSpecialKeyvals[keymap.KeyCtrl]

	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyDown(ctrl): %v", err)
	}
	bridge.events = nil
	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}

	if downs := countGnomeKeyEvents(bridge.events, "down", ctrlKey); downs != 0 {
		t.Fatalf("ctrl pressed %d times during the combination, want 0\n%v", downs, bridge.events)
	}
	if ups := countGnomeKeyEvents(bridge.events, "up", ctrlKey); ups != 0 {
		t.Fatalf("ctrl released %d times during the combination, want 0\n%v", ups, bridge.events)
	}
	if !b.held.ctrl {
		t.Fatal("caller's held ctrl was forgotten after a combination that also used it")
	}
}

// A combination must still press and release a modifier the caller is not
// holding.
func TestGnomeCombinationStillReleasesItsOwnModifier(t *testing.T) {
	bridge := &recordingGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	ctx := context.Background()
	ctrlKey := gnomeSpecialKeyvals[keymap.KeyCtrl]

	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}
	if downs := countGnomeKeyEvents(bridge.events, "down", ctrlKey); downs != 1 {
		t.Fatalf("ctrl pressed %d times, want 1\n%v", downs, bridge.events)
	}
	if ups := countGnomeKeyEvents(bridge.events, "up", ctrlKey); ups != 1 {
		t.Fatalf("ctrl released %d times, want 1\n%v", ups, bridge.events)
	}
	if b.held.ctrl {
		t.Fatal("a combination left ctrl held")
	}
}

// KeyUp clears the held state so a later combination owns the modifier again.
func TestGnomeKeyUpClearsHeldModifier(t *testing.T) {
	bridge := &recordingGnomeBridge{}
	b := &GnomeNativeBackend{bridge: bridge}
	ctx := context.Background()
	ctrlKey := gnomeSpecialKeyvals[keymap.KeyCtrl]

	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyDown(ctrl): %v", err)
	}
	if err := b.KeyUp(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyUp(ctrl): %v", err)
	}
	if b.held.ctrl {
		t.Fatal("ctrl still held after KeyUp")
	}
	bridge.events = nil
	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}
	if downs := countGnomeKeyEvents(bridge.events, "down", ctrlKey); downs != 1 {
		t.Fatalf("ctrl pressed %d times after KeyUp, want 1\n%v", downs, bridge.events)
	}
}

// A failed combination must release only the modifiers it brought down, never
// one the caller was holding.
func TestGnomeFailureReleasesOnlyItsOwnModifiers(t *testing.T) {
	shiftKey := gnomeSpecialKeyvals[keymap.KeyShift]
	ctrlKey := gnomeSpecialKeyvals[keymap.KeyCtrl]

	// The caller holds shift; the combination additionally needs ctrl and then
	// fails. Only ctrl may be released.
	failing := &recordingGnomeBridge{}
	failing.failOn = func(state string, keyval uint32) error {
		if state == "down" && keyval != shiftKey && keyval != ctrlKey {
			return fmt.Errorf("synthetic bridge failure")
		}
		return nil
	}
	b := &GnomeNativeBackend{bridge: failing}
	if err := b.KeyDown(context.Background(), "shift"); err != nil {
		t.Fatalf("KeyDown(shift): %v", err)
	}
	failing.events = nil
	if err := b.Type(context.Background(), "{ctrl+a}"); err == nil {
		t.Fatal("Type succeeded despite the failing bridge")
	}
	if ups := countGnomeKeyEvents(failing.events, "up", shiftKey); ups != 0 {
		t.Fatalf("the caller's shift was released %d times on failure\n%v", ups, failing.events)
	}
	if ups := countGnomeKeyEvents(failing.events, "up", ctrlKey); ups == 0 {
		t.Fatalf("the combination's own ctrl was not released on failure\n%v", failing.events)
	}
	if !b.held.shift {
		t.Fatal("the caller's held shift was forgotten after a failed combination")
	}
}

// The keymap accepts several spellings for each modifier. Held-state tracking
// switched on the raw string, so a modifier the caller held under an alias was
// invisible and the next combination released it mid-gesture.
func TestGnomeHeldModifierRecognizesEveryAcceptedSpelling(t *testing.T) {
	for _, name := range []string{
		"ctrl", "control", "control_l", "CTRL", "Control_L",
		"shift", "shift_l", "SHIFT",
		"alt", "alt_l", "Alt_L",
		"super", "meta", "logo", "super_l",
	} {
		t.Run(name, func(t *testing.T) {
			held := modifiers{}
			updateHeldModifier(&held, name, true)
			if !held.any() {
				t.Fatalf("%q resolved to a modifier but was not recorded as held", name)
			}
			updateHeldModifier(&held, name, false)
			if held.any() {
				t.Fatalf("%q was not cleared on release: %+v", name, held)
			}
		})
	}

	// A key that is not a modifier must not touch the held set.
	held := modifiers{ctrl: true}
	updateHeldModifier(&held, "a", true)
	if !held.ctrl || held.shift || held.alt || held.super {
		t.Fatalf("a non-modifier key changed the held set: %+v", held)
	}
}

// A duplicate down for a modifier the caller already holds acquires nothing. The
// error path releases what this call pressed, so claiming it would drop a hold the
// caller set up before the call started and leave their gesture broken.
func TestGnomeDuplicateDownDoesNotReleaseAPreheldModifier(t *testing.T) {
	for _, spelling := range []string{"ctrl", "shift", "alt", "super"} {
		t.Run(spelling, func(t *testing.T) {
			// Fail the literal that follows, so the duplicate down has already been
			// processed when the call fails.
			bridge := &recordingGnomeBridge{textErr: fmt.Errorf("injected failure")}
			b := &GnomeNativeBackend{bridge: bridge}
			ctx := context.Background()

			if err := b.KeyDown(ctx, spelling); err != nil {
				t.Fatalf("KeyDown(%s): %v", spelling, err)
			}
			keyval := gnomeModifierKeyvalForTest(spelling)
			bridge.events = nil

			// The modifier goes down again inside the call, then the call fails.
			if err := b.Type(ctx, "{"+spelling+" down}x"); err == nil {
				t.Fatal("Type succeeded, want the injected failure")
			}

			if ups := countGnomeKeyEvents(bridge.events, "up", keyval); ups != 0 {
				t.Fatalf("%s released %d time(s) by a call that never acquired it\n%v", spelling, ups, bridge.events)
			}
			if !modifierIsHeld(b.held, spelling) {
				t.Fatalf("%s is no longer recorded as held after the failed call", spelling)
			}
		})
	}
}

// gnomeModifierKeyvalForTest returns the keyval the backend uses for a modifier
// name, so a test can assert on the physical key events.
func gnomeModifierKeyvalForTest(name string) uint32 {
	resolved, ok := keymap.FromString(name)
	if !ok {
		return 0
	}
	return gnomeSpecialKeyvals[resolved]
}

// "win" is accepted in combo syntax, so a caller may hold the modifier under that
// name. Held-modifier tracking resolves names through the keymap, so a name it
// lacks is invisible and a later combination presses and then releases the
// caller's own key.
func TestGnomeHeldModifierRecognizesWinAlias(t *testing.T) {
	held := modifiers{}
	updateHeldModifier(&held, "win", true)
	if !modifierIsHeld(held, "win") {
		t.Fatal(`"win" is accepted in combo syntax but was not recorded as a held modifier`)
	}
	if !held.super {
		t.Fatalf(`"win" did not resolve to the super modifier: %+v`, held)
	}
	updateHeldModifier(&held, "win", false)
	if held.any() {
		t.Fatalf(`"win" was not cleared on release: %+v`, held)
	}

	// Every name the combo parser accepts must resolve, or the two disagree about
	// what a modifier is called.
	for _, name := range []string{"ctrl", "control", "control_l", "alt", "alt_l", "shift", "shift_l", "super", "meta", "logo", "super_l", "win"} {
		if !isModifierName(name) {
			continue
		}
		if _, ok := keymap.FromString(name); !ok {
			t.Errorf("combo parser accepts modifier %q but the keymap does not resolve it", name)
		}
	}
}

type cancelAfterGNOMEActionBridge struct {
	recordingGnomeBridge
	cancel      context.CancelFunc
	cancelOnKey uint32
	textCalls   int
}

func (b *cancelAfterGNOMEActionBridge) Key(ctx context.Context, keyval uint32, pressed bool) error {
	err := b.recordingGnomeBridge.Key(ctx, keyval, pressed)
	if pressed && keyval == b.cancelOnKey {
		b.cancel()
	}
	return err
}
func (b *cancelAfterGNOMEActionBridge) Text(_ context.Context, _ string) error {
	b.textCalls++
	b.cancel()
	return nil
}

// A bridge may complete a successful first event just as the caller cancels.
// A typed sequence must not dispatch any subsequent private input and must
// release only the modifier the canceled operation acquired.
func TestGnomeTypeStopsAfterCancellationBetweenActions(t *testing.T) {
	ctrl := gnomeSpecialKeyvals[keymap.KeyCtrl]
	for _, tc := range []struct {
		name, sequence string
		cancelOnKey    uint32
		wantKeyDown    int
		wantTextCalls  int
	}{
		{"after modifier press", "{ctrl down}{ctrl+a}", ctrl, 1, 0},
		{"after literal text", "prefix{enter}", 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bridge := &cancelAfterGNOMEActionBridge{cancel: cancel, cancelOnKey: tc.cancelOnKey}
			backend := &GnomeNativeBackend{bridge: bridge}
			err := backend.Type(ctx, tc.sequence)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled sequence returned %v", err)
			}
			if bridge.textCalls != tc.wantTextCalls {
				t.Fatalf("unexpected text commands=%d", bridge.textCalls)
			}
			if countGnomeKeyEvents(bridge.events, "down", ctrl) != tc.wantKeyDown {
				t.Fatalf("unexpected modifier press: %v", bridge.events)
			}
			if countGnomeKeyEvents(bridge.events, "up", ctrl) != tc.wantKeyDown {
				t.Fatalf("caller cancellation did not clean up acquired modifier: %v", bridge.events)
			}
			if countGnomeKeyEvents(bridge.events, "down", uint32('a')) != 0 || countGnomeKeyEvents(bridge.events, "down", 0xff0d) != 0 {
				t.Fatalf("subsequent private keys dispatched after cancel: %v", bridge.events)
			}
			if backend.held.any() {
				t.Fatalf("canceled sequence retained phantom modifiers: %+v", backend.held)
			}
		})
	}
}
