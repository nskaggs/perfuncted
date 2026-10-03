package input

import (
	"context"
	"fmt"
	"testing"

	"github.com/nskaggs/perfuncted/internal/keymap"
)

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
