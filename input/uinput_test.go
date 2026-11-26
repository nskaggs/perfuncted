package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/bendahl/uinput"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/output"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestUinputReportsLogicalCoordinatesOnlyAfterSingleOutputDiscovery(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister = oldOpen; newUinputBackend = oldCreate })
	cases := []struct {
		name       string
		outputs    []output.Info
		w, h       int32
		advertised bool
	}{
		{name: "authoritative single fractional output", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}, advertised: true},
		{name: "multiple outputs remain unproven", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}}, {Available: true, Geometry: output.Geometry{X: 1600, W: 1280, H: 720}}}, advertised: false},
		{name: "explicit dimensions without authoritative output", w: 1600, h: 900, advertised: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) {
				return &fakeUinputOutputLister{outputs: tc.outputs}, nil
			}
			newUinputBackend = func(int32, int32) (Inputter, error) { return &UinputBackend{}, nil }
			backend, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, tc.w, tc.h)
			if err != nil {
				t.Fatal(err)
			}
			b, ok := backend.(*UinputBackend)
			if !ok {
				t.Fatalf("unexpected Uinput backend %T", backend)
			}
			coordinates, err := b.PointerCoordinateSpace(context.Background())
			if tc.advertised {
				if err != nil || coordinates.Kind != CoordinateSpaceLogical || coordinates.ScaleX != 1 || coordinates.ScaleY != 1 {
					t.Fatalf("coordinates=%+v err=%v", coordinates, err)
				}
			} else if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unproven coordinate mapping was advertised: %+v err=%v", coordinates, err)
			}
			if got := slices.Contains(b.SupportedOperations(), "pointer-coordinate-space"); got != tc.advertised {
				t.Fatalf("operations advertisement=%v want=%v", got, tc.advertised)
			}
		})
	}
}

// A canceled composed key request must stop before consulting the keyboard
// device or attempting to press temporary modifiers.
func TestUinputComposedKeyDispatchRejectsCanceledRequestBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.typeKeyWithMods(ctx, 1, false, false, modifiers{shift: true, ctrl: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled key request reached device dispatch: %v", err)
	}
}

// A writable regular file is not the kernel uinput device. Probe must not
// advertise hardware event authority based merely on successful open(2).
func TestUinputProbeRejectsRegularFileMasqueradingAsDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uinput")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	result := checkUinputPath(path)
	if result.Available || !strings.Contains(result.Reason, "device") {
		t.Fatalf("regular file advertised as uinput device: %+v", result)
	}
}

// A symlink to an unrelated character device is not proof that the kernel's
// uinput endpoint exists. Probing must inspect the path itself, not its target.
func TestUinputProbeRejectsSymlinkToUnrelatedCharacterDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uinput")
	if err := os.Symlink("/dev/null", path); err != nil {
		t.Fatal(err)
	}
	result := checkUinputPath(path)
	if result.Available || !strings.Contains(result.Reason, "device") {
		t.Fatalf("symlink to unrelated character device advertised as uinput: %+v", result)
	}
}

// Transport-level mouse helpers must honor cancellation before examining an
// unavailable virtual device or emitting any pointer events. This also holds
// for calls made after the public dispatch layer's admission check.
func TestUinputInternalMouseHelpersRefuseCanceledDispatch(t *testing.T) {
	var backend UinputBackend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"button down", func() error { return backend.mouseDown(ctx, 1) }},
		{"button up", func() error { return backend.mouseUp(ctx, 1) }},
		{"scroll", func() error { return backend.scroll(ctx, false, 1, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled helper reached device dispatch: %v", err)
			}
		})
	}
}

// Malformed wheel requests must be rejected before the backend tries to
// allocate its lazy mouse device, even without an attached uinput transport.
func TestUinputPublicScrollDirectionsRejectInvalidCountsBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(int) error
	}{
		{"up", func(n int) error { return backend.ScrollUp(ctx, n) }},
		{"down", func(n int) error { return backend.ScrollDown(ctx, n) }},
		{"left", func(n int) error { return backend.ScrollLeft(ctx, n) }},
		{"right", func(n int) error { return backend.ScrollRight(ctx, n) }},
	} {
		for _, n := range []int{-1, maxScrollClicks + 1} {
			t.Run(tc.name+"/invalid", func(t *testing.T) {
				err := tc.call(n)
				if err == nil || !strings.Contains(err.Error(), "scroll clicks") {
					t.Fatalf("invalid count %d was dispatched: %v", n, err)
				}
			})
		}
	}
}

// Internal helpers are also required to validate button IDs independently
// of public API preflight. An unavailable touchpad must not mask the error.
func TestUinputInternalMouseButtonsRejectInvalidIDsBeforeDeviceAccess(t *testing.T) {
	var backend UinputBackend
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(int) error
	}{
		{"down", func(n int) error { return backend.mouseDown(ctx, n) }},
		{"up", func(n int) error { return backend.mouseUp(ctx, n) }},
	} {
		for _, id := range []int{-1, 4} {
			t.Run(tc.name+"/invalid", func(t *testing.T) {
				err := tc.call(id)
				if err == nil || !strings.Contains(err.Error(), "button") {
					t.Fatalf("invalid mouse button %d was dispatched: %v", id, err)
				}
			})
		}
	}
}

// A writable character device is not necessarily the uinput protocol.
// Reporting an unrelated device as input-capable breaks backend selection.
func TestUinputProbeRejectsWritableNonUinputCharacterDevice(t *testing.T) {
	if _, err := os.Stat("/dev/null"); err != nil {
		t.Skipf("no synthetic character device: %v", err)
	}
	got := checkUinputPath("/dev/null")
	if got.Available {
		t.Fatalf("unrelated writable character device advertised as uinput: %+v", got)
	}
	if got.Name != "uinput" || !strings.Contains(got.Reason, "uinput") {
		t.Fatalf("missing diagnostic for unrelated device: %+v", got)
	}
}

// Probing an unavailable /dev/uinput candidate must distinguish an absent
// device from a malformed path, and must not advertise permission or protocol
// support when no device has been successfully opened and identified.
func TestUinputProbeMissingDeviceAndInvalidParentFailClosed(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing-uinput")
	parent := filepath.Join(root, "ordinary-file")
	if err := os.WriteFile(parent, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, reason string }{
		{"missing device", missing, "not found"},
		{"parent is regular file", filepath.Join(parent, "uinput"), "not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkUinputPath(tc.path)
			if got.Name != "uinput" || got.Available || !strings.Contains(strings.ToLower(got.Reason), tc.reason) {
				t.Fatalf("inaccessible path accepted or misdiagnosed: %+v", got)
			}
		})
	}
	data, err := os.ReadFile(parent)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("probe altered an unrelated parent: %q err=%v", data, err)
	}
}

// The internal wheel dispatch is also a security boundary: callers may
// reach it only with a live operation context and validated click count.
// Neither revoked work nor an invalid wheel request can cause a device write,
// including on a lazily allocated pointer device.
func TestUinputScrollInternalAuthorizationRejectsRevokedOrInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name       string
		horizontal bool
		delta      int
		clicks     int
		canceled   bool
	}{
		{name: "revoked vertical", delta: 1, clicks: 1, canceled: true},
		{name: "revoked horizontal", horizontal: true, delta: -1, clicks: -7, canceled: true},
		{name: "invalid negative vertical", delta: 1, clicks: -1},
		{name: "invalid negative horizontal", horizontal: true, delta: -1, clicks: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mouse := &recordingMouse{}
			b := &UinputBackend{mouse: mouse}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			err := b.scroll(ctx, tc.horizontal, tc.delta, tc.clicks)
			if tc.canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("revoked operation returned %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "scroll") {
				t.Fatalf("invalid scroll was admitted: %v", err)
			}
			if events := mouse.eventsSnapshot(); len(events) != 0 {
				t.Fatalf("unauthorized scroll wrote %v", events)
			}
		})
	}
}

// countKeyEvents counts "down:<code>" and "up:<code>" entries recorded by
// recordingKeyboard.
func countKeyEvents(events []string, kind string, code int) int {
	prefix := kind + ":" + strconv.Itoa(code)
	n := 0
	for _, e := range events {
		if e == prefix {
			n++
		}
	}
	return n
}

// A modifier the caller is holding through KeyDown must survive a combination
// or literal text that also needs it. Releasing it ends the caller's gesture
// partway, so the characters typed afterwards arrive without it.
func TestUinputCombinationPreservesCallerHeldModifier(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyDown(ctrl): %v", err)
	}
	if err := b.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("Type({ctrl+s}): %v", err)
	}

	downs := countKeyEvents(kb.events, "down", uinput.KeyLeftctrl)
	ups := countKeyEvents(kb.events, "up", uinput.KeyLeftctrl)
	if downs != 1 || ups != 0 {
		t.Fatalf("ctrl key events: %d down, %d up; want 1 down and 0 up so the caller's hold survives\n%v",
			downs, ups, kb.events)
	}
	if !b.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("caller's held ctrl was forgotten after a combination that also used it")
	}
}

// A combination must still press and release a modifier the caller is not
// holding, so ordinary typing is unaffected.
func TestUinputCombinationStillReleasesItsOwnModifier(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("Type({ctrl+s}): %v", err)
	}
	downs := countKeyEvents(kb.events, "down", uinput.KeyLeftctrl)
	ups := countKeyEvents(kb.events, "up", uinput.KeyLeftctrl)
	if downs != 1 || ups != 1 {
		t.Fatalf("ctrl key events: %d down, %d up; want exactly one of each\n%v", downs, ups, kb.events)
	}
	if b.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("a combination left ctrl held")
	}
}

// Literal text that needs shift must not release a shift the caller holds.
func TestUinputLiteralTextPreservesCallerHeldShift(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.KeyDown(ctx, "shift"); err != nil {
		t.Fatalf("KeyDown(shift): %v", err)
	}
	if err := b.TypeLiteral(ctx, "ABC"); err != nil {
		t.Fatalf("TypeLiteral(ABC): %v", err)
	}

	if ups := countKeyEvents(kb.events, "up", uinput.KeyLeftshift); ups != 0 {
		t.Fatalf("shift was released %d times while the caller held it\n%v", ups, kb.events)
	}
	if !b.modifierHeld(uinput.KeyLeftshift) {
		t.Fatal("caller's held shift was forgotten after literal text that also needed it")
	}
}

// Literal text still presses and releases its own shift when the caller holds
// none, so uppercase characters continue to work.
func TestUinputLiteralTextStillPressesItsOwnShift(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.TypeLiteral(ctx, "A"); err != nil {
		t.Fatalf("TypeLiteral(A): %v", err)
	}
	if downs := countKeyEvents(kb.events, "down", uinput.KeyLeftshift); downs != 1 {
		t.Fatalf("shift pressed %d times, want 1\n%v", downs, kb.events)
	}
	if ups := countKeyEvents(kb.events, "up", uinput.KeyLeftshift); ups != 1 {
		t.Fatalf("shift released %d times, want 1\n%v", ups, kb.events)
	}
}

// KeyUp must clear the held state, so a later combination is free to press and
// release the modifier again.
func TestUinputKeyUpClearsHeldModifier(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.KeyDown(ctx, "alt"); err != nil {
		t.Fatalf("KeyDown(alt): %v", err)
	}
	if err := b.KeyUp(ctx, "alt"); err != nil {
		t.Fatalf("KeyUp(alt): %v", err)
	}
	if b.modifierHeld(uinput.KeyLeftalt) {
		t.Fatal("alt is still held after KeyUp")
	}
	if err := b.Type(ctx, "{alt+f}"); err != nil {
		t.Fatalf("Type({alt+f}): %v", err)
	}
	if b.modifierHeld(uinput.KeyLeftalt) {
		t.Fatalf("a combination left alt held\n%v", kb.events)
	}
}

// Non-modifier keys must not be tracked as modifiers.
func TestUinputNonModifierKeysAreNotTracked(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}
	ctx := context.Background()

	if err := b.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("KeyDown(a): %v", err)
	}
	for code := range modifierBits {
		if b.modifierHeld(code) {
			t.Fatalf("plain key press marked modifier %d as held", code)
		}
	}
	if err := b.KeyUp(ctx, "a"); err != nil {
		t.Fatalf("KeyUp(a): %v", err)
	}
}

// The modifier set must cover both sides of every modifier with a distinct bit.
func TestModifierBitsCoverLeftAndRightVariants(t *testing.T) {
	for _, code := range []int{
		uinput.KeyLeftshift, uinput.KeyRightshift,
		uinput.KeyLeftctrl, uinput.KeyRightctrl,
		uinput.KeyLeftalt, uinput.KeyRightalt,
		uinput.KeyLeftmeta, uinput.KeyRightmeta,
	} {
		if modifierBit(code) == 0 {
			t.Fatalf("modifier %d has no bit", code)
		}
	}
	if modifierBit(uinput.KeyA) != 0 {
		t.Fatal("a plain key has a modifier bit")
	}
	seen := map[uint32]int{}
	for code, bit := range modifierBits {
		if bit == 0 {
			t.Fatalf("modifier %d maps to an empty bit", code)
		}
		if other, dup := seen[bit]; dup {
			t.Fatalf("modifiers %d and %d share bit %d", other, code, bit)
		}
		seen[bit] = code
	}
	if len(seen) != len(modifierBits) {
		t.Fatalf("modifier table has %d entries but %d distinct bits", len(modifierBits), len(seen))
	}
}

func TestTemporaryModifiersExcludesHeldOnes(t *testing.T) {
	b := &UinputBackend{}
	requested := []int{uinput.KeyLeftshift, uinput.KeyLeftctrl, uinput.KeyLeftalt}

	b.markModifierHeld(uinput.KeyLeftctrl)
	got := b.temporaryModifiers(requested)
	want := []int{uinput.KeyLeftshift, uinput.KeyLeftalt}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("temporaryModifiers = %v, want %v", got, want)
	}

	// Holding none means every requested modifier is this operation's own.
	b2 := &UinputBackend{}
	if got := b2.temporaryModifiers(requested); len(got) != len(requested) {
		t.Fatalf("temporaryModifiers with nothing held = %v, want all %v", got, requested)
	}

	// Holding all means this operation presses nothing.
	for _, mk := range requested {
		b2.markModifierHeld(mk)
	}
	if got := b2.temporaryModifiers(requested); len(got) != 0 {
		t.Fatalf("temporaryModifiers with everything held = %v, want none", got)
	}
}

// A modifier held through the Type down/up syntax has to reach the same
// bookkeeping KeyDown uses. Otherwise it stayed invisible to modifierHeld and the
// next operation pressed it again instead of recognising it was already down.
func TestUinputTypeDownRecordsHeldModifier(t *testing.T) {
	kb := &recordingKeyboard{}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}

	if err := b.Type(context.Background(), "{ctrl down}"); err != nil {
		t.Fatalf("Type down: %v", err)
	}
	if !b.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("a modifier held through Type was not recorded as held")
	}
	requested := []int{uinput.KeyLeftctrl, uinput.KeyLeftshift}
	if got := b.temporaryModifiers(requested); len(got) != 1 || got[0] != uinput.KeyLeftshift {
		t.Fatalf("temporaryModifiers = %v, want only the shift; the held ctrl was pressed again", got)
	}

	if err := b.Type(context.Background(), "{ctrl up}"); err != nil {
		t.Fatalf("Type up: %v", err)
	}
	if b.modifierHeld(uinput.KeyLeftctrl) {
		t.Fatal("a modifier released through Type was still recorded as held")
	}
}

// The GNOME backend released the keys it had pressed when a call failed, but left
// the held set saying they were down. The next call then skipped pressing a
// modifier that was no longer down.
func TestGnomeTypeFailureClearsHeldModifiersItReleased(t *testing.T) {
	held := modifiers{ctrl: true}
	clearHeldModifiers(&held, modifiers{ctrl: true})
	if held.any() {
		t.Fatalf("held = %+v, want the released modifier cleared", held)
	}

	// A modifier this call did not press must survive the cleanup.
	held = modifiers{ctrl: true, shift: true}
	clearHeldModifiers(&held, modifiers{ctrl: true})
	if !held.shift || held.ctrl {
		t.Fatalf("held = %+v, want shift kept and ctrl cleared", held)
	}

	var nilHeld *modifiers
	clearHeldModifiers(nilHeld, modifiers{ctrl: true})
}

type fakeUinputOutputLister struct {
	outputs []output.Info
	err     error
	closed  bool
}

func (f *fakeUinputOutputLister) List(context.Context) ([]output.Info, error) {
	return f.outputs, f.err
}
func (f *fakeUinputOutputLister) Close() error { f.closed = true; return nil }

func TestRuntimeUinputResolvesFractionalLogicalOutputBeforeCreatingDevice(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister = oldOpen; newUinputBackend = oldCreate })
	l := &fakeUinputOutputLister{outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}}
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) { return l, nil }
	calls := 0
	newUinputBackend = func(w, h int32) (Inputter, error) {
		calls++
		if w != 1600 || h != 900 {
			t.Errorf("virtual touchpad axes %dx%d, want logical 1600x900", w, h)
		}
		return noopInputter{}, nil
	}
	_, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
	if err != nil || calls != 1 || !l.closed {
		t.Fatalf("err=%v calls=%d outputClosed=%v", err, calls, l.closed)
	}
}
func TestRuntimeUinputFailsClosedWithoutOutputGeometry(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister = oldOpen; newUinputBackend = oldCreate })
	l := &fakeUinputOutputLister{outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 0, H: 0}}}}
	openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) { return l, nil }
	newUinputBackend = func(int32, int32) (Inputter, error) {
		t.Fatal("invalid device must not be created")
		return nil, errors.New("should not happen")
	}
	_, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, 0, 0)
	if err == nil || !l.closed {
		t.Fatalf("err=%v closed=%v", err, l.closed)
	}
}
