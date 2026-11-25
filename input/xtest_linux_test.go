//go:build linux

package input

import (
	"bufio"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
	"github.com/nskaggs/perfuncted/internal/x11test"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestXTestAltGrPropagatesTransientKeymapFailureAndRecovers(t *testing.T) {
	failure := errors.New("synthetic X11 keyboard mapping failure")
	reads := 0
	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 8}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			reads++
			if reads == 1 {
				return x11.NewMockGetKeyboardMappingErrorCookie(failure)
			}
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: 1,
				Keysyms:           []xproto.Keysym{isoLevel3ShiftKeysym},
			})
		},
	}
	backend := &XTestBackend{conn: conn}
	if code, err := backend.altGrKeycode(); code != 0 || !errors.Is(err, failure) {
		t.Fatalf("failed keymap resolved AltGr as %d, err=%v", code, err)
	}
	if code, err := backend.altGrKeycode(); code != 8 || err != nil {
		t.Fatalf("AltGr did not recover after transient failure: code=%d err=%v", code, err)
	}
	if reads != 2 {
		t.Fatalf("keymap read %d times, want one failed and one successful read", reads)
	}
}

func TestXTestCanceledPressPreservesCancellationWhenReleaseFails(t *testing.T) {
	releaseErr := errors.New("synthetic input release failure")
	tests := []struct {
		name  string
		call  func(*XTestBackend, context.Context) error
		press xtestEvent
		up    xtestEvent
		want  []xtestEvent
	}{
		{
			name: "mouse click",
			call: func(b *XTestBackend, ctx context.Context) error {
				return b.MouseClick(ctx, 4, 7, 1)
			},
			press: xtestEvent{eventType: xproto.ButtonPress, detail: 1},
			up:    xtestEvent{eventType: xproto.ButtonRelease, detail: 1},
			want: []xtestEvent{
				{eventType: xproto.MotionNotify},
				{eventType: xproto.ButtonPress, detail: 1},
				{eventType: xproto.ButtonRelease, detail: 1},
			},
		},
		{
			name: "typed key",
			call: func(b *XTestBackend, ctx context.Context) error {
				return b.Type(ctx, "{a}")
			},
			press: xtestEvent{eventType: xproto.KeyPress, detail: 8},
			up:    xtestEvent{eventType: xproto.KeyRelease, detail: 8},
			want: []xtestEvent{
				{eventType: xproto.KeyPress, detail: 8},
				{eventType: xproto.KeyRelease, detail: 8},
			},
		},
		{
			name: "literal text",
			call: func(b *XTestBackend, ctx context.Context) error {
				return b.TypeLiteral(ctx, "a")
			},
			press: xtestEvent{eventType: xproto.KeyPress, detail: 8},
			up:    xtestEvent{eventType: xproto.KeyRelease, detail: 8},
			want: []xtestEvent{
				{eventType: xproto.KeyPress, detail: 8},
				{eventType: xproto.KeyRelease, detail: 8},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(event xtestEvent) error {
				if event == test.press {
					cancel()
				}
				if event == test.up {
					return releaseErr
				}
				return nil
			})
			t.Cleanup(func() { _ = b.Close() })

			err := test.call(b, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled action error = %v, want context.Canceled", err)
			}
			if !errors.Is(err, releaseErr) {
				t.Fatalf("canceled action error = %v, want release failure", err)
			}
			if got := *events; !sameXTestEvents(got, test.want) {
				t.Fatalf("press/release cleanup events = %#v", got)
			}
		})
	}
}

func TestXTestTypeStopsDuringPreflightBeforeAnyWireInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []xtestEvent
	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 9}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			cancel()
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: 1,
				Keysyms:           []xproto.Keysym{'a', 0xffe3},
			})
		},
		FakeInputCheckedFunc: func(eventType, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
			events = append(events, xtestEvent{eventType: eventType, detail: detail})
			return x11.NewMockXTestFakeInputCookie(nil)
		},
	}
	backend := &XTestBackend{conn: conn, root: 1}

	err := backend.Type(ctx, "a{ctrl}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Type error = %v, want cancellation during batch preflight", err)
	}
	if len(events) != 0 {
		t.Fatalf("canceled preflight emitted input events: %+v", events)
	}
}

func TestXTestTypeStopsBeforeNextActionAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a', 0xffe3}, func(event xtestEvent) error {
		if event == (xtestEvent{xproto.KeyRelease, 8}) {
			cancel()
		}
		return nil
	})

	err := backend.Type(ctx, "a{ctrl}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Type error = %v, want cancellation before the next action", err)
	}
	want := []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyRelease, 8}}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("Type events = %+v, want only the completed first key action %+v", *events, want)
	}
}

func TestXTestTypeLiteralStopsBetweenRunesAfterBalancedKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a', 'b'}, func(event xtestEvent) error {
		if event == (xtestEvent{xproto.KeyRelease, 8}) {
			cancel()
		}
		return nil
	})

	err := backend.TypeLiteral(ctx, "ab")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("TypeLiteral error = %v, want cancellation between runes", err)
	}
	want := []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyRelease, 8}}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("TypeLiteral events = %+v, want only a balanced first rune %+v", *events, want)
	}
}

func TestXTestTypedModifierReleaseFailureRetainsHeldOwnership(t *testing.T) {
	releaseErr := errors.New("synthetic XTEST release failure")
	failRelease := false
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe3}, func(event xtestEvent) error {
		if failRelease && event == (xtestEvent{xproto.KeyRelease, 8}) {
			return releaseErr
		}
		return nil
	})
	if err := backend.Type(context.Background(), "{ctrl down}"); err != nil {
		t.Fatalf("hold Ctrl: %v", err)
	}
	*events = nil
	failRelease = true
	err := backend.Type(context.Background(), "{ctrl up}")
	if !errors.Is(err, releaseErr) {
		t.Fatalf("typed release error = %v, want %v", err, releaseErr)
	}
	if !reflect.DeepEqual(*events, []xtestEvent{{xproto.KeyRelease, 8}}) {
		t.Fatalf("typed release events = %+v, want one failed release", *events)
	}
	if !backend.modifierHeld(8) {
		t.Fatal("failed typed release cleared caller-owned modifier state")
	}
}

func TestXTestMouseClickDoesNotClickWhenPointerMoveFails(t *testing.T) {
	moveErr := errors.New("synthetic pointer movement failure")
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a'}, func(event xtestEvent) error {
		if event.eventType == xproto.MotionNotify {
			return moveErr
		}
		return nil
	})

	err := backend.MouseClick(context.Background(), 20, 30, 1)
	if !errors.Is(err, moveErr) {
		t.Fatalf("MouseClick error = %v, want movement failure %v", err, moveErr)
	}
	want := []xtestEvent{{eventType: xproto.MotionNotify}}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("MouseClick events = %+v, want movement only %+v", *events, want)
	}
}

// Duplicate key-down on an already-owned modifier must not cause an XTEST
// key repeat. A later KeyUp remains the sole release of the caller's hold.
func TestXTestDirectHeldModifierCannotBePressedTwice(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	if err := b.KeyDown(context.Background(), "ctrl"); err != nil {
		t.Fatal(err)
	}
	initial := len(*events)
	if err := b.KeyDown(context.Background(), "ctrl"); err == nil || !strings.Contains(err.Error(), "already held") {
		t.Fatalf("duplicate ctrl down accepted: %v", err)
	}
	if len(*events) != initial {
		t.Fatalf("duplicate ctrl down emitted input: %+v", (*events)[initial:])
	}
	if err := b.KeyUp(context.Background(), "ctrl"); err != nil {
		t.Fatalf("original hold could not be released: %v", err)
	}
}

// Reject invalid modifier ownership transitions over the *entire* command
// before emitting a valid prefix, even when the invalid transition appears
// after text or an accelerator.
func TestXTestTypeRejectsDuplicateHeldModifierWithoutPartialInput(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"duplicate down", "a{ctrl down}{ctrl down}"},
		{"tap explicitly held modifier", "a{ctrl down}{ctrl}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newModifierXTestBackend(t)
			err := b.Type(context.Background(), tc.command)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("invalid command accepted: %v", err)
			}
			if len(*events) != 0 {
				t.Fatalf("failed command injected prefix: %+v", *events)
			}
		})
	}
}

func TestXTestKeyNameResolutionPrintableASCIIAndUntrustedBytes(t *testing.T) {
	backend := &XTestBackend{keymap: map[xproto.Keysym]keycodeLevel{
		xproto.Keysym('~'):    {keycode: 47},
		xproto.Keysym('a'):    {keycode: 38},
		keysymForName["ctrl"]: {keycode: 37},
	}}
	for _, tc := range []struct {
		name string
		want xproto.Keycode
	}{
		{"~", 47}, {"a", 38}, {"ctrl", 37},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := backend.keycodeFor(tc.name)
			if err != nil || got != tc.want {
				t.Fatalf("key=%q got=%d error=%v, want=%d", tc.name, got, err, tc.want)
			}
		})
	}
	for _, key := range []string{"", "unrecognized-key", "\x00", "\x1f", "\x7f", "\xff"} {
		t.Run("invalid-"+key, func(t *testing.T) {
			got, err := backend.keycodeFor(key)
			if got != 0 || err == nil || !strings.Contains(err.Error(), "unknown key") {
				t.Fatalf("unsupported key=%q accepted as keycode=%d error=%v", key, got, err)
			}
		})
	}
	missing := &XTestBackend{keymap: map[xproto.Keysym]keycodeLevel{}}
	if got, err := missing.keycodeFor("~"); got != 0 || err == nil || !strings.Contains(err.Error(), "not found in keymap") {
		t.Fatalf("unmapped printable key=%d err=%v", got, err)
	}
}

// A transient malformed keyboard map must be reported without panicking and
// must not be cached, allowing a later valid map to recover key injection.
func TestXTestKeyboardMappingNilSuccessReplyFailsClosedAndCanRecover(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
	mock, ok := backend.conn.(*x11.MockConnection)
	if !ok {
		t.Fatalf("unexpected X11 transport %T", backend.conn)
	}
	queries := 0
	mock.GetKeyboardMappingFunc = func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
		queries++
		if queries == 1 {
			return x11.NewMockGetKeyboardMappingCookie(nil)
		}
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{0x61}})
	}
	err := backend.KeyDown(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "keyboard mapping") {
		t.Fatalf("nil map reply accepted: %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("failed map injected events: %+v", *events)
	}
	if err := backend.KeyDown(context.Background(), "a"); err != nil {
		t.Fatalf("recovered map query: %v", err)
	}
	if queries != 2 || len(*events) != 1 || (*events)[0].eventType != xproto.KeyPress {
		t.Fatalf("map retry=%d events=%+v", queries, *events)
	}
}

// A reachable X server without the XTEST extension must not grant input
// authority or leave a partially initialized backend with the caller.
func TestXTestBackendRejectsLiveServerWithoutXTestExtension(t *testing.T) {
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb is unavailable")
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "Xvfb", "-screen", "0", "640x480x24", "-ac", "-extension", "XTEST", "-displayfd", "3")
	cmd.ExtraFiles = []*os.File{write}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		_ = write.Close()
		t.Fatalf("start Xvfb without XTEST: %v", err)
	}
	_ = write.Close()
	t.Cleanup(func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(read).ReadString('\n')
	if err != nil {
		t.Fatalf("Xvfb display identity: %v (%s)", err, stderr.String())
	}
	display := ":" + strings.TrimSpace(line)
	if len(display) < 2 {
		t.Fatalf("invalid Xvfb display %q", display)
	}
	conn, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatalf("X server not reachable at %s: %v (%s)", display, err, stderr.String())
	}
	defer conn.Close()
	backend, err := NewXTestBackend(display)
	if backend != nil {
		_ = backend.Close()
		t.Fatal("backend initialized without the XTEST extension")
	}
	if err == nil || !strings.Contains(err.Error(), "init XTEST") {
		t.Fatalf("missing extension returned error %v, want XTEST initialization rejection", err)
	}
}

// countXTestEvents counts recorded XTEST events of a kind for a keycode detail.
func countXTestEvents(events []xtestEvent, eventType byte, detail byte) int {
	n := 0
	for _, e := range events {
		if e.eventType == eventType && e.detail == detail {
			n++
		}
	}
	return n
}

// keycodeForCtrl resolves the ctrl keycode the same way the backend does, so
// the assertions do not hardcode a keymap-specific value.
func keycodeForCtrl(t *testing.T, b *XTestBackend) byte {
	t.Helper()
	kc, err := b.keycodeFor("ctrl")
	if err != nil {
		t.Fatalf("keycodeFor(ctrl): %v", err)
	}
	return byte(kc)
}

// newModifierXTestBackend builds a backend over a keymap that resolves every
// modifier plus a plain key, with one keysym per keycode so the keycodes are
// consecutive from MinKeycode.
func newModifierXTestBackend(t *testing.T) (*XTestBackend, *[]xtestEvent) {
	t.Helper()
	return newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, nil)
}

// A modifier the caller is holding through KeyDown must survive a combination
// that also needs it.
func TestXTestCombinationPreservesCallerHeldModifier(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	ctx := context.Background()
	ctrl := keycodeForCtrl(t, b)

	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyDown(ctrl): %v", err)
	}
	*events = nil
	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}

	downs := countXTestEvents(*events, xproto.KeyPress, ctrl)
	ups := countXTestEvents(*events, xproto.KeyRelease, ctrl)
	if downs != 0 || ups != 0 {
		t.Fatalf("ctrl key events during the combination: %d press, %d release; want none so the caller's hold survives\n%v",
			downs, ups, *events)
	}
	if !b.modifierHeld(xproto.Keycode(ctrl)) {
		t.Fatal("caller's held ctrl was forgotten after a combination that also used it")
	}
}

// A combination must still press and release a modifier the caller is not
// holding.
func TestXTestCombinationStillReleasesItsOwnModifier(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	ctx := context.Background()
	ctrl := keycodeForCtrl(t, b)

	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, ctrl); downs != 1 {
		t.Fatalf("ctrl pressed %d times, want 1\n%v", downs, *events)
	}
	if ups := countXTestEvents(*events, xproto.KeyRelease, ctrl); ups != 1 {
		t.Fatalf("ctrl released %d times, want 1\n%v", ups, *events)
	}
	if b.modifierHeld(xproto.Keycode(ctrl)) {
		t.Fatal("a combination left ctrl held")
	}
}

// KeyUp clears the held state so a later combination owns the modifier again.
func TestXTestKeyUpClearsHeldModifier(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	ctx := context.Background()
	ctrl := keycodeForCtrl(t, b)

	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyDown(ctrl): %v", err)
	}
	if err := b.KeyUp(ctx, "ctrl"); err != nil {
		t.Fatalf("KeyUp(ctrl): %v", err)
	}
	if b.modifierHeld(xproto.Keycode(ctrl)) {
		t.Fatal("ctrl is still held after KeyUp")
	}
	*events = nil
	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("Type({ctrl+a}): %v", err)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, ctrl); downs != 1 {
		t.Fatalf("ctrl pressed %d times after KeyUp, want 1\n%v", downs, *events)
	}
	if ups := countXTestEvents(*events, xproto.KeyRelease, ctrl); ups != 1 {
		t.Fatalf("ctrl released %d times after KeyUp, want 1\n%v", ups, *events)
	}
}

// Only modifier keycodes are tracked as held.
func TestXTestNonModifierKeysAreNotTracked(t *testing.T) {
	b, _ := newModifierXTestBackend(t)
	ctx := context.Background()

	if err := b.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("KeyDown(a): %v", err)
	}
	for kc := range b.modifierCodeSet() {
		if b.modifierHeld(kc) {
			t.Fatalf("plain key press marked modifier %d as held", kc)
		}
	}
}

// temporaryModifiers drops the ones the caller already holds.
func TestXTestTemporaryModifiersExcludesHeldOnes(t *testing.T) {
	b, _ := newModifierXTestBackend(t)
	requested, err := b.temporaryModifierKeycodes(modifiers{shift: true, ctrl: true, alt: true, super: true})
	if err != nil {
		t.Fatalf("temporaryModifierKeycodes: %v", err)
	}
	if len(requested) == 0 {
		t.Fatal("no modifier keycodes resolved")
	}

	if got := b.temporaryModifiers(requested); len(got) != len(requested) {
		t.Fatalf("temporaryModifiers with nothing held = %v, want all %v", got, requested)
	}

	b.markModifierHeld(requested[0])
	got := b.temporaryModifiers(requested)
	if len(got) != len(requested)-1 {
		t.Fatalf("temporaryModifiers = %v, want %v without the held modifier", got, requested)
	}
	for _, kc := range got {
		if kc == requested[0] {
			t.Fatal("temporaryModifiers kept a modifier the caller is holding")
		}
	}
}

// The modifier code set must resolve every modifier a combination can request.
func TestXTestModifierCodeSetCoversAllModifiers(t *testing.T) {
	b, _ := newModifierXTestBackend(t)
	set := b.modifierCodeSet()
	for _, name := range modifierNames(modifiers{shift: true, ctrl: true, alt: true, super: true}) {
		kc, err := b.keycodeFor(name)
		if err != nil {
			t.Fatalf("keycodeFor(%q): %v", name, err)
		}
		if !set[kc] {
			t.Fatalf("modifier %q (keycode %d) is missing from the held-modifier set", name, kc)
		}
	}
}

// germanLikeKeysyms is a four-level keymap in the order GetKeyboardMapping
// reports them: unshifted, shifted, AltGr, AltGr+Shift. The first keycode is
// mapped to ISO_Level3_Shift the way a German layout maps the right Alt.
func germanLikeKeysyms() []xproto.Keysym {
	return []xproto.Keysym{
		// ctrl
		0xffe3, 0, 0, 0,
		// shift
		0xffe1, 0, 0, 0,
		// ISO_Level3_Shift, the Mode_switch key
		isoLevel3ShiftKeysym, 0, 0, 0,
		// alt
		0xffe9, 0, 0, 0,
		// e, E, Euro sign, at sign
		0x65, 0x45, 0x20ac, 0x40,
		// s, S, sharp s, question mark
		0x73, 0x53, 0xdf, 0x3f,
	}
}

const keycodesPerKeycode4 = 4

// A keysym at the AltGr level must be produced by pressing the layout's
// Mode_switch key. Pressing Shift, as the level index alone used to imply,
// delivers a different character with no error.
func TestXTestLevelThreeUsesModeSwitchNotShift(t *testing.T) {
	b, events := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	ctx := context.Background()

	shiftCode, err := b.keycodeFor("shift")
	if err != nil {
		t.Fatalf("keycodeFor(shift): %v", err)
	}
	altGrCode, err := b.altGrKeycode()
	if err != nil {
		t.Fatalf("altGrKeycode: %v", err)
	}
	if altGrCode == shiftCode {
		t.Fatalf("altGrKeycode = %d, which is also the shift keycode", altGrCode)
	}

	if err := b.TypeLiteral(ctx, "€"); err != nil {
		t.Fatalf("TypeLiteral(Euro sign): %v", err)
	}

	if ups := countXTestEvents(*events, xproto.KeyRelease, byte(shiftCode)); ups != 0 {
		t.Fatalf("shift released %d times typing an AltGr keysym, want 0\n%v", ups, *events)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, byte(altGrCode)); downs != 1 {
		t.Fatalf("Mode_shift pressed %d times, want 1\n%v", downs, *events)
	}
	if ups := countXTestEvents(*events, xproto.KeyRelease, byte(altGrCode)); ups != 1 {
		t.Fatalf("Mode_shift released %d times, want 1\n%v", ups, *events)
	}
}

// The second level is the only shifted one, so an uppercase letter still uses
// Shift and nothing else.
func TestXTestLevelTwoStillUsesShift(t *testing.T) {
	b, events := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	ctx := context.Background()

	shiftCode, err := b.keycodeFor("shift")
	if err != nil {
		t.Fatalf("keycodeFor(shift): %v", err)
	}
	altGrCode, err := b.altGrKeycode()
	if err != nil {
		t.Fatalf("altGrKeycode: %v", err)
	}

	if err := b.TypeLiteral(ctx, "E"); err != nil {
		t.Fatalf("TypeLiteral(E): %v", err)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, byte(shiftCode)); downs != 1 {
		t.Fatalf("shift pressed %d times, want 1\n%v", downs, *events)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, byte(altGrCode)); downs != 0 {
		t.Fatalf("Mode_shift pressed %d times for a shifted keysym, want 0\n%v", downs, *events)
	}
}

// A layout with no Mode_shift key cannot type a level-three keysym. Reporting
// that is better than sending a key combination that produces another character.
func TestXTestAltGrWithoutModeShiftKeyIsReported(t *testing.T) {
	// Three levels per keycode, so the Euro sign has no Mode_shift to press.
	b, _ := newTestXTestBackend(t, 3, []xproto.Keysym{
		0xffe3, 0xffe1, 0xffe9, // ctrl, shift, alt
		0x65, 0x45, 0x20ac, // e, E, Euro sign
	}, nil)

	if _, err := b.altGrKeycode(); err == nil {
		t.Fatal("altGrKeycode resolved without a Mode_shift key")
	}
	if err := b.TypeLiteral(context.Background(), "€"); err == nil {
		t.Fatal("TypeLiteral typed an AltGr keysym on a layout without Mode_shift")
	}
}

// Level one needs no modifiers at all.
func TestXTestLevelOneNeedsNoModifiers(t *testing.T) {
	b, _ := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	codes, err := b.levelModifiers(0)
	if err != nil {
		t.Fatalf("levelModifiers(0): %v", err)
	}
	if len(codes) != 0 {
		t.Fatalf("level 0 = %v, want no modifiers", codes)
	}
}

// A level beyond the four X11 defines is rejected rather than guessed.
func TestXTestLevelBeyondFourIsRejected(t *testing.T) {
	b, _ := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	if _, err := b.levelModifiers(4); err == nil {
		t.Fatal("level 4 was accepted")
	}
}

// A caller holding shift does not have it pressed or released again for a
// shifted character.
func TestXTestShiftedCharacterPreservesCallerHeldShift(t *testing.T) {
	b, events := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
	ctx := context.Background()

	shiftCode, err := b.keycodeFor("shift")
	if err != nil {
		t.Fatalf("keycodeFor(shift): %v", err)
	}
	if err := b.KeyDown(ctx, "shift"); err != nil {
		t.Fatalf("KeyDown(shift): %v", err)
	}
	*events = nil
	if err := b.TypeLiteral(ctx, "E"); err != nil {
		t.Fatalf("TypeLiteral(E): %v", err)
	}
	if downs := countXTestEvents(*events, xproto.KeyPress, byte(shiftCode)); downs != 0 {
		t.Fatalf("shift pressed %d times, want 0\n%v", downs, *events)
	}
	if ups := countXTestEvents(*events, xproto.KeyRelease, byte(shiftCode)); ups != 0 {
		t.Fatalf("shift released %d times, want 0\n%v", ups, *events)
	}
	if !b.modifierHeld(shiftCode) {
		t.Fatal("caller's held shift was forgotten")
	}
}

// levelModifiers applies the layout's Mode_switch key to reach keysyms behind
// AltGr. Because modifierCodeSet is what recognises an already-held modifier by
// keycode, the Mode_switch keycode has to be in that set: without it, a caller
// holding AltGr has it pressed again on every character and released again after
// each one, so the caller's own hold is broken by ordinary text.
func TestXTestModifierCodeSetIncludesLayoutModeSwitchKey(t *testing.T) {
	b, _ := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)

	altGr, err := b.altGrKeycode()
	if err != nil {
		t.Fatalf("altGrKeycode: %v", err)
	}
	if !b.modifierCodeSet()[altGr] {
		t.Fatalf("Mode_switch keycode %d is not in the modifier code set; a caller holding AltGr would have it re-pressed per character", altGr)
	}
	if b.modifierHeld(altGr) {
		t.Fatal("Mode_switch keycode reported as held before anything pressed it")
	}
	b.markModifierHeld(altGr)
	if !b.modifierHeld(altGr) {
		t.Fatal("a Mode_shift key the caller is holding was not recognised")
	}

	// A character behind AltGr must then leave the caller's hold alone.
	required, err := b.levelModifiers(2)
	if err != nil {
		t.Fatalf("levelModifiers(2): %v", err)
	}
	for _, kc := range b.temporaryModifiers(required) {
		if kc == altGr {
			t.Fatal("temporaryModifiers wants to press a Mode_shift key the caller already holds")
		}
	}
}

func TestXTestPointerLocationFailsClosedOnEmptySuccessfulReply(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
	mock, ok := backend.conn.(*x11.MockConnection)
	if !ok {
		t.Fatalf("unexpected X11 synthetic transport %T", backend.conn)
	}
	mock.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie { return syntheticPointerReply{} }
	x, y, err := backend.PointerLocation(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty") || x != 0 || y != 0 {
		t.Fatalf("empty pointer reply returned x=%d y=%d err=%v", x, y, err)
	}
	if len(*events) != 0 {
		t.Fatalf("pointer query injected input events: %+v", *events)
	}
}

// A rejected automation command must never type a valid prefix into the
// focused desktop before discovering a malformed or unreachable final key.
func TestXTestTypeRejectsUnreachableTailBeforeInjectingValidPrefix(t *testing.T) {
	for _, tc := range []struct{ name, text, want string }{
		{"named key unavailable", "a{f12}", "key"},
		{"literal Unicode unavailable", "aΩ", "character"},
		{"modifier key missing", "a{alt+a}", "key"},
		{"shifted key unsupported by layout", "aA", "character"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
			err := b.Type(context.Background(), tc.text)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unreachable tail %q returned %v", tc.text, err)
			}
			if len(*events) != 0 {
				t.Fatalf("unreachable tail injected %d XTEST events: %+v", len(*events), *events)
			}
		})
	}
}
func TestXTestTypeLiteralRejectsUnreachableTailBeforeInjectingValidPrefix(t *testing.T) {
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
	err := b.TypeLiteral(context.Background(), "aΩ")
	if err == nil || !strings.Contains(err.Error(), "character") {
		t.Fatalf("unreachable literal accepted: %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("unreachable literal injected events: %+v", *events)
	}
}

type privateXvfbEventResult struct {
	event xgb.Event
	err   error
}

func TestXTestBackendAgainstPrivateXvfb(t *testing.T) {
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb is unavailable")
	}
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Fatalf("start private Xvfb: %v", err)
	}
	t.Cleanup(stop)
	observer, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatalf("connect event observer to private Xvfb: %v", err)
	}
	readerDone := make(chan struct{})
	readerStarted := false
	t.Cleanup(func() {
		observer.Close()
		if readerStarted {
			<-readerDone
		}
	})
	screen := xproto.Setup(observer).DefaultScreen(observer)
	window, err := xproto.NewWindowId(observer)
	if err != nil {
		t.Fatalf("allocate observer window: %v", err)
	}
	eventMask := uint32(xproto.EventMaskKeyPress | xproto.EventMaskKeyRelease | xproto.EventMaskButtonPress | xproto.EventMaskButtonRelease | xproto.EventMaskPointerMotion)
	if err := xproto.CreateWindowChecked(observer, screen.RootDepth, window, screen.Root, 0, 0, 200, 200, 0, xproto.WindowClassInputOutput, screen.RootVisual, xproto.CwEventMask, []uint32{eventMask}).Check(); err != nil {
		t.Fatalf("create observer window: %v", err)
	}
	if err := xproto.MapWindowChecked(observer, window).Check(); err != nil {
		t.Fatalf("map observer window: %v", err)
	}
	if err := xproto.SetInputFocusChecked(observer, xproto.InputFocusNone, window, xproto.TimeCurrentTime).Check(); err != nil {
		t.Fatalf("focus observer window: %v", err)
	}
	observer.Sync()
	events := make(chan privateXvfbEventResult, 64)
	readerStarted = true
	go func() {
		defer close(readerDone)
		for {
			event, err := observer.WaitForEvent()
			events <- privateXvfbEventResult{event: event, err: err}
			if event == nil || err != nil {
				return
			}
		}
	}()

	backend, err := NewXTestBackend(display)
	if err != nil {
		t.Fatalf("connect XTEST backend to private Xvfb: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := backend.MouseMove(ctx, 113, 57); err != nil {
		t.Fatalf("move pointer on private Xvfb: %v", err)
	}
	if x, y, err := backend.PointerLocation(ctx); err != nil || x != 113 || y != 57 {
		t.Fatalf("pointer after XTEST motion = (%d,%d), %v; want (113,57)", x, y, err)
	}
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.MotionNotify, 0)
	keycode, err := backend.keycodeFor("a")
	if err != nil {
		t.Fatalf("resolve XTEST keycode: %v", err)
	}
	if err := backend.KeyDown(ctx, "a"); err != nil {
		t.Fatalf("press key on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.KeyPress, byte(keycode))
	if err := backend.KeyUp(ctx, "a"); err != nil {
		t.Fatalf("release key on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.KeyRelease, byte(keycode))
	if err := backend.MouseClick(ctx, 113, 57, 1); err != nil {
		t.Fatalf("click on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.ButtonPress, 1)
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.ButtonRelease, 1)
	if err := backend.MouseDown(ctx, 1); err != nil {
		t.Fatalf("press button on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.ButtonPress, 1)
	if err := backend.MouseUp(ctx, 1); err != nil {
		t.Fatalf("release button on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.ButtonRelease, 1)
	for _, scroll := range []struct {
		name string
		call func(context.Context, int) error
		btn  byte
	}{
		{name: "up", call: backend.ScrollUp, btn: 4},
		{name: "down", call: backend.ScrollDown, btn: 5},
		{name: "left", call: backend.ScrollLeft, btn: 6},
		{name: "right", call: backend.ScrollRight, btn: 7},
	} {
		if err := scroll.call(ctx, 1); err != nil {
			t.Fatalf("scroll %s on private Xvfb: %v", scroll.name, err)
		}
		awaitPrivateXvfbEvent(ctx, t, events, window, xproto.ButtonPress, scroll.btn)
		awaitPrivateXvfbEvent(ctx, t, events, window, xproto.ButtonRelease, scroll.btn)
	}
	if err := backend.TypeLiteral(ctx, "a"); err != nil {
		t.Fatalf("type literal on private Xvfb: %v", err)
	}
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.KeyPress, byte(keycode))
	awaitPrivateXvfbEvent(ctx, t, events, window, xproto.KeyRelease, byte(keycode))
	if err := backend.Sync(ctx); err != nil {
		t.Fatalf("synchronize private Xvfb input: %v", err)
	}
}

func awaitPrivateXvfbEvent(ctx context.Context, t *testing.T, events <-chan privateXvfbEventResult, window xproto.Window, eventType, detail byte) {
	t.Helper()
	for {
		select {
		case result := <-events:
			if result.err != nil {
				t.Fatalf("read event from private Xvfb: %v", result.err)
			}
			if result.event == nil {
				t.Fatal("private Xvfb event stream closed before expected input")
			}
			var gotType, gotDetail byte
			var gotWindow xproto.Window
			switch event := result.event.(type) {
			case xproto.KeyPressEvent:
				gotType, gotDetail, gotWindow = xproto.KeyPress, byte(event.Detail), event.Event
			case xproto.KeyReleaseEvent:
				gotType, gotDetail, gotWindow = xproto.KeyRelease, byte(event.Detail), event.Event
			case xproto.ButtonPressEvent:
				gotType, gotDetail, gotWindow = xproto.ButtonPress, byte(event.Detail), event.Event
			case xproto.ButtonReleaseEvent:
				gotType, gotDetail, gotWindow = xproto.ButtonRelease, byte(event.Detail), event.Event
			case xproto.MotionNotifyEvent:
				gotType, gotWindow = xproto.MotionNotify, event.Event
			}
			if gotType == eventType && gotDetail == detail && gotWindow == window {
				return
			}
		case <-ctx.Done():
			t.Fatalf("no private Xvfb event type=%d detail=%d window=%d: %v", eventType, detail, window, ctx.Err())
		}
	}
}

func TestXTestKeyUpRejectsUnknownKeyBeforeWireDispatch(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a'}, nil)
	t.Cleanup(func() { _ = backend.Close() })

	if err := backend.KeyUp(context.Background(), "unmapped-key"); err == nil {
		t.Fatal("unknown KeyUp was accepted")
	}
	if len(*events) != 0 {
		t.Fatalf("unknown KeyUp reached XTEST: %+v", *events)
	}
}

func TestXTestKeyUpFailurePreservesHeldModifierOwnership(t *testing.T) {
	const ctrlKeycode = xproto.Keycode(8)
	releaseErr := errors.New("synthetic XTEST key-release failure")
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe3}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyRelease {
			return releaseErr
		}
		return nil
	})
	t.Cleanup(func() { _ = backend.Close() })
	backend.markModifierHeld(ctrlKeycode)

	if err := backend.KeyUp(context.Background(), "ctrl"); !errors.Is(err, releaseErr) {
		t.Fatalf("KeyUp error = %v, want release failure", err)
	}
	if !backend.modifierHeld(ctrlKeycode) {
		t.Fatal("failed KeyUp discarded caller-owned modifier state")
	}
	want := []xtestEvent{{eventType: xproto.KeyRelease, detail: byte(ctrlKeycode)}}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("KeyUp wire events = %+v, want %+v", *events, want)
	}
}

func TestXTestClickCancellationAfterPressStillReleasesButton(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a'}, func(event xtestEvent) error {
		if event.eventType == xproto.ButtonPress {
			cancel()
		}
		return nil
	})
	t.Cleanup(func() { _ = backend.Close() })

	if err := backend.MouseClick(ctx, 12, 34, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("MouseClick error = %v, want context.Canceled", err)
	}
	want := []xtestEvent{
		{eventType: xproto.MotionNotify},
		{eventType: xproto.ButtonPress, detail: 1},
		{eventType: xproto.ButtonRelease, detail: 1},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("canceled click wire events = %+v, want balanced press/release %+v", *events, want)
	}
}

func TestXTestKeyboardSetupFailsClosedOnMissingOrInvertedKeycodeRange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup *xproto.SetupInfo
	}{
		{"missing", nil},
		{"inverted", &xproto.SetupInfo{MinKeycode: 12, MaxKeycode: 8}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
			conn, ok := b.conn.(*x11.MockConnection)
			if !ok {
				t.Fatalf("unexpected X11 connection %T", b.conn)
			}
			conn.SetupFunc = func() *xproto.SetupInfo { return tc.setup }
			calls := 0
			conn.GetKeyboardMappingFunc = func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
				calls++
				return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{0x61}})
			}
			err := b.KeyDown(context.Background(), "a")
			if err == nil || !strings.Contains(err.Error(), "keyboard") {
				t.Fatalf("invalid setup accepted: %v", err)
			}
			if calls != 0 || len(*events) != 0 {
				t.Fatalf("invalid setup queried map %d times, injected %+v", calls, *events)
			}
		})
	}
}

// Explicit holds have no later tap-release phase. A failed XTEST press may
// already have reached the server, so failed holds must compensate without
// claiming the modifier is owned by the caller.
func TestXTestExplicitKeyDownCompensatesForUncertainPress(t *testing.T) { //nolint:gocyclo // Two entrypoints and two key kinds cover independent hold-ownership transitions.
	for _, tc := range []struct {
		name, key                     string
		useType, releaseFails, cancel bool
	}{
		{name: "KeyDown modifier", key: "ctrl"},
		{name: "KeyDown normal key", key: "a", releaseFails: true},
		{name: "Type explicit modifier down", key: "ctrl", useType: true, cancel: true},
		{name: "Type explicit normal down", key: "a", useType: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("explicit key-down write was uncertain")
			releaseErr := errors.New("compensating key-up failed")
			code := byte(8)
			if tc.key == "a" {
				code = 9
			}
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe3, 0x61}, func(ev xtestEvent) error {
				if ev.eventType == xproto.KeyPress && ev.detail == code {
					if tc.cancel {
						cancel()
					}
					return pressErr
				}
				if ev.eventType == xproto.KeyRelease && ev.detail == code && tc.releaseFails {
					return releaseErr
				}
				return nil
			})
			t.Cleanup(func() { _ = b.Close() })
			var err error
			if tc.useType {
				err = b.Type(ctx, "{"+tc.key+" down}")
			} else {
				err = b.KeyDown(ctx, tc.key)
			}
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("lost press, cleanup or cancellation error: %v", err)
			}
			want := []xtestEvent{{eventType: xproto.KeyPress, detail: code}, {eventType: xproto.KeyRelease, detail: code}}
			if !sameXTestEvents(*events, want) {
				t.Fatalf("failed explicit hold events=%#v, want %#v", *events, want)
			}
			if b.modifierHeld(xproto.Keycode(8)) {
				t.Fatal("failed hold marked Ctrl as caller-owned")
			}
		})
	}
}

// An explicit MouseDown can reach the X server before its checked request
// reports an error. A failed hold must attempt a release, not leave a button
// stuck, and must preserve cancellation and any release failure.
func TestXTestMouseDownCompensatesForUncertainPress(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		releaseFails, cancel bool
	}{
		{name: "release succeeds"},
		{name: "release fails", releaseFails: true},
		{name: "canceled after press", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("XTEST button press acknowledged with error")
			releaseErr := errors.New("XTEST button release failed")
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
				if ev.eventType == xproto.ButtonPress {
					if tc.cancel {
						cancel()
					}
					return pressErr
				}
				if ev.eventType == xproto.ButtonRelease && tc.releaseFails {
					return releaseErr
				}
				return nil
			})
			t.Cleanup(func() { _ = b.Close() })
			err := b.MouseDown(ctx, 1)
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("failed hold did not preserve error(s): %v", err)
			}
			want := []xtestEvent{{eventType: xproto.ButtonPress, detail: 1}, {eventType: xproto.ButtonRelease, detail: 1}}
			if !sameXTestEvents(*events, want) {
				t.Fatalf("failed hold events=%#v, want %#v", *events, want)
			}
		})
	}
}

// XTEST can deliver KeyPress before reporting a protocol error. A typed key
// must receive a compensating KeyRelease even when its press failed.
func TestXTestTypedKeyCompensatesForUncertainPress(t *testing.T) {
	for _, method := range []struct {
		name string
		call func(*XTestBackend, context.Context) error
	}{
		{name: "literal", call: func(b *XTestBackend, ctx context.Context) error { return b.TypeLiteral(ctx, "a") }},
		{name: "key syntax", call: func(b *XTestBackend, ctx context.Context) error { return b.Type(ctx, "{a}") }},
	} {
		for _, tc := range []struct {
			name                 string
			releaseFails, cancel bool
		}{
			{name: "release succeeds"},
			{name: "release fails", releaseFails: true},
			{name: "cancellation during press", cancel: true},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("XTEST key press may have been delivered")
				releaseErr := errors.New("XTEST key cleanup failed")
				b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(event xtestEvent) error {
					if event.eventType == xproto.KeyPress {
						if tc.cancel {
							cancel()
						}
						return pressErr
					}
					if event.eventType == xproto.KeyRelease && tc.releaseFails {
						return releaseErr
					}
					return nil
				})
				t.Cleanup(func() { _ = b.Close() })
				err := method.call(b, ctx)
				if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("press/cleanup/cancellation result: %v", err)
				}
				want := []xtestEvent{{eventType: xproto.KeyPress, detail: 8}, {eventType: xproto.KeyRelease, detail: 8}}
				if !sameXTestEvents(*events, want) {
					t.Fatalf("uncompensated key events: got %#v, want %#v", *events, want)
				}
			})
		}
	}
}

// When XTEST reports an error on a temporary modifier press, that press may
// already have reached the X server. Compensating release must precede any
// prior modifiers' cleanup, even on request cancellation.
func TestXTestTemporaryModifierCompensatesForUncertainPress(t *testing.T) { //nolint:gocyclo // Six cases verify release order and joined failures across two modifier paths.
	for _, method := range []struct {
		name         string
		kpk          byte
		keysyms      []xproto.Keysym
		key          string
		failCode     byte
		previousCode byte
	}{
		{name: "key syntax ctrl then shift", kpk: 1, keysyms: []xproto.Keysym{0xffe1, 0xffe3, 0xffe9, 0xffeb, 0x61}, key: "{ctrl+shift+a}", failCode: 9, previousCode: 8},
		{name: "literal shifted key", kpk: 2, keysyms: []xproto.Keysym{0xffe1, 0, 0x61, 0x41}, key: "A", failCode: 8},
	} {
		for _, tc := range []struct {
			name                 string
			releaseFails, cancel bool
		}{
			{name: "cleanup succeeds"},
			{name: "cleanup fails", releaseFails: true},
			{name: "canceled during failed press", cancel: true},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("modifier write delivered then rejected")
				releaseErr := errors.New("failed uncertain modifier cleanup")
				b, events := newTestXTestBackend(t, method.kpk, method.keysyms, func(ev xtestEvent) error {
					if ev.eventType == xproto.KeyPress && ev.detail == method.failCode {
						if tc.cancel {
							cancel()
						}
						return pressErr
					}
					if ev.eventType == xproto.KeyRelease && ev.detail == method.failCode && tc.releaseFails {
						return releaseErr
					}
					return nil
				})
				t.Cleanup(func() { _ = b.Close() })
				var err error
				if method.previousCode != 0 {
					err = b.Type(ctx, method.key)
				} else {
					err = b.TypeLiteral(ctx, method.key)
				}
				if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("lost press, cleanup or cancellation error: %v", err)
				}
				want := []xtestEvent{}
				if method.previousCode != 0 {
					want = append(want, xtestEvent{eventType: xproto.KeyPress, detail: method.previousCode})
				}
				want = append(want, xtestEvent{eventType: xproto.KeyPress, detail: method.failCode}, xtestEvent{eventType: xproto.KeyRelease, detail: method.failCode})
				if method.previousCode != 0 {
					want = append(want, xtestEvent{eventType: xproto.KeyRelease, detail: method.previousCode})
				}
				if !sameXTestEvents(*events, want) {
					t.Fatalf("modifier events=%#v, want %#v", *events, want)
				}
			})
		}
	}
}

// An XTEST request can be processed before the X server reports a protocol
// error. When a click's button-down acknowledgement fails, attempt button-up
// instead of leaving the external button state uncertain.
func TestXTestClickCompensatesForUncertainButtonPressFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		releaseFails bool
		cancel       bool
	}{
		{name: "release succeeds"},
		{name: "release fails", releaseFails: true},
		{name: "canceled during press failure", cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pressErr := errors.New("synthetic XTEST button press error")
			releaseErr := errors.New("synthetic XTEST button release error")
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(event xtestEvent) error {
				if event.eventType == xproto.ButtonPress {
					if tc.cancel {
						cancel()
					}
					return pressErr
				}
				if event.eventType == xproto.ButtonRelease && tc.releaseFails {
					return releaseErr
				}
				return nil
			})
			t.Cleanup(func() { _ = b.Close() })
			err := b.MouseClick(ctx, 11, 12, 1)
			if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
				t.Fatalf("lost press, release, or cancellation failure: %v", err)
			}
			want := []xtestEvent{
				{eventType: xproto.MotionNotify},
				{eventType: xproto.ButtonPress, detail: 1},
				{eventType: xproto.ButtonRelease, detail: 1},
			}
			if !sameXTestEvents(*events, want) {
				t.Fatalf("button cleanup events=%#v want=%#v", *events, want)
			}
		})
	}
}

// XTEST wheel-button presses can reach X11 before a transport failure is
// reported. No later scrolls are permitted and a possible press must receive
// a compensating release, including after caller cancellation.
func TestXTestScrollCompensatesForUncertainWheelPress(t *testing.T) { //nolint:gocyclo // Four directions and multiple failure modes exercise the same wire contract.
	for _, direction := range []struct {
		name   string
		button byte
		scroll func(*XTestBackend, context.Context, int) error
	}{
		{"up", 4, (*XTestBackend).ScrollUp},
		{"down", 5, (*XTestBackend).ScrollDown},
		{"left", 6, (*XTestBackend).ScrollLeft},
		{"right", 7, (*XTestBackend).ScrollRight},
	} {
		for _, tc := range []struct {
			name                 string
			releaseFails, cancel bool
		}{
			{name: "release succeeds"},
			{name: "release fails", releaseFails: true},
			{name: "cancel during failed press", cancel: true},
		} {
			t.Run(direction.name+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				pressErr := errors.New("XTEST wheel press may have been accepted")
				releaseErr := errors.New("XTEST wheel cleanup failed")
				b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
					if ev.eventType == xproto.ButtonPress && ev.detail == direction.button {
						if tc.cancel {
							cancel()
						}
						return pressErr
					}
					if ev.eventType == xproto.ButtonRelease && ev.detail == direction.button && tc.releaseFails {
						return releaseErr
					}
					return nil
				})
				t.Cleanup(func() { _ = b.Close() })
				err := direction.scroll(b, ctx, 3)
				if !errors.Is(err, pressErr) || errors.Is(err, releaseErr) != tc.releaseFails || (tc.cancel && !errors.Is(err, context.Canceled)) {
					t.Fatalf("lost press, cleanup or cancellation failure: %v", err)
				}
				want := []xtestEvent{{eventType: xproto.ButtonPress, detail: direction.button}, {eventType: xproto.ButtonRelease, detail: direction.button}}
				if !sameXTestEvents(*events, want) {
					t.Fatalf("wheel failure injected %#v, want %#v", *events, want)
				}
			})
		}
	}
}

// After a successful key press, a failed release may never have reached the
// X server. Retry cleanup before returning instead of leaving a key held.
func TestXTestTypedKeyReleaseFailureRetriesCleanup(t *testing.T) { //nolint:gocyclo // Two typing APIs and two release failure modes.
	for _, method := range []struct {
		name string
		call func(*XTestBackend) error
	}{
		{name: "literal", call: func(b *XTestBackend) error { return b.TypeLiteral(context.Background(), "a") }},
		{name: "key syntax", call: func(b *XTestBackend) error { return b.Type(context.Background(), "{a}") }},
	} {
		for _, tc := range []struct {
			name      string
			permanent bool
		}{
			{name: "transient release failure"},
			{name: "permanent release failure", permanent: true},
		} {
			t.Run(method.name+"/"+tc.name, func(t *testing.T) {
				firstErr := errors.New("key release not acknowledged")
				cleanupErr := errors.New("compensating key release failed")
				releases := 0
				b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
					if ev.eventType == xproto.KeyRelease {
						releases++
						if releases == 1 {
							return firstErr
						}
						if tc.permanent {
							return cleanupErr
						}
					}
					return nil
				})
				t.Cleanup(func() { _ = b.Close() })
				err := method.call(b)
				if !errors.Is(err, firstErr) || errors.Is(err, cleanupErr) != tc.permanent {
					t.Fatalf("key release errors not preserved: %v", err)
				}
				want := []xtestEvent{{eventType: xproto.KeyPress, detail: 8}, {eventType: xproto.KeyRelease, detail: 8}, {eventType: xproto.KeyRelease, detail: 8}}
				if !sameXTestEvents(*events, want) {
					t.Fatalf("release events=%#v want=%#v", *events, want)
				}
			})
		}
	}
}

// No XTEST input event may be injected for an unheld modifier: a release is a
// state transition owned by the caller, not permission to affect other users'
// keyboard state on the shared X server.
func TestXTestUnheldModifiersDoNotInjectKeyUpEvents(t *testing.T) {
	modifiers := []string{"shift", "ctrl", "alt", "super"}
	mapping := []xproto.Keysym{0xffe1, 0xffe3, 0xffe9, 0xffeb}
	for _, name := range modifiers {
		t.Run(name, func(t *testing.T) {
			backend, events := newTestXTestBackend(t, 1, mapping, nil)
			err := backend.KeyUp(context.Background(), name)
			if err == nil || !strings.Contains(err.Error(), "not held") {
				t.Fatalf("unheld modifier key-up accepted: %v", err)
			}
			if len(*events) != 0 {
				t.Fatalf("unheld modifier produced events: %+v", *events)
			}
		})
	}
}

func TestXTestUnheldModifierSyntaxDoesNotInjectKeyUpEvents(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe1, 0xffe3, 0xffe9, 0xffeb}, nil)
	err := backend.Type(context.Background(), "{ctrl up}")
	if err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("unheld Ctrl syntax accepted: %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("unheld modifier syntax injected %+v", *events)
	}
}

// A malformed release at the tail must reject the entire command before a
// valid prefix can type into a different window.
func TestXTestUnheldModifierTailFailsBeforeTypingPrefix(t *testing.T) {
	backend, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61, 0xffe3}, nil)
	err := backend.Type(context.Background(), "a{ctrl up}")
	if err == nil || !strings.Contains(err.Error(), "not held") {
		t.Fatalf("invalid suffix did not fail: %v", err)
	}
	if len(*events) != 0 {
		t.Fatalf("invalid suffix left partially typed prefix: %+v", *events)
	}
}

// A successful X server reply with no keysyms per keycode cannot be used for
// injection or cached. Once a valid map arrives, the same live backend must
// recover without retaining the malformed layout as authority.
func TestXTestZeroKeysymsPerKeycodeFailsClosedThenRecovers(t *testing.T) {
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{'a'}, nil)
	conn, ok := b.conn.(*x11.MockConnection)
	if !ok {
		t.Fatalf("unexpected test connection: %T", b.conn)
	}
	requests := 0
	conn.GetKeyboardMappingFunc = func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
		requests++
		if requests <= 2 {
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 0, Keysyms: []xproto.Keysym{'a'}})
		}
		return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{'a'}})
	}
	for i := 0; i < 2; i++ {
		if err := b.KeyDown(context.Background(), "a"); err == nil || !strings.Contains(err.Error(), "keysyms_per_keycode=0") {
			t.Fatalf("malformed server keyboard mapping attempt %d accepted: %v", i, err)
		}
		if len(*events) != 0 || b.keymap != nil {
			t.Fatalf("malformed keyboard mapping authorized key events=%+v cache=%+v", *events, b.keymap)
		}
	}
	if err := b.KeyDown(context.Background(), "a"); err != nil {
		t.Fatalf("recovered X11 map rejected: %v", err)
	}
	if requests != 3 || len(*events) != 1 || (*events)[0].eventType != xproto.KeyPress {
		t.Fatalf("transient keyboard map not retried: requests=%d events=%+v", requests, *events)
	}
}

type xtestEvent struct {
	eventType byte
	detail    byte
}

type orderingXTestConnection struct {
	*x11.MockConnection

	mu           sync.Mutex
	events       []xtestEvent
	firstStarted chan struct{}
	firstRelease chan struct{}
	firstOnce    func()
	closeCalls   int
}

var _ x11.Connection = (*orderingXTestConnection)(nil)

func newOrderingXTestConnection(keysyms []xproto.Keysym) *orderingXTestConnection {
	firstStarted := make(chan struct{})
	return &orderingXTestConnection{
		MockConnection: &x11.MockConnection{
			SetupFunc: func() *xproto.SetupInfo {
				return &xproto.SetupInfo{
					MinKeycode: 8,
					MaxKeycode: 8 + xproto.Keycode(len(keysyms)) - 1,
				}
			},
			GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
				return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
					KeysymsPerKeycode: 1,
					Keysyms:           keysyms,
				})
			},
		},
		firstStarted: firstStarted,
		firstRelease: make(chan struct{}),
		firstOnce:    sync.OnceFunc(func() { close(firstStarted) }),
	}
}

func (c *orderingXTestConnection) FakeInputChecked(eventType byte, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
	c.mu.Lock()
	c.events = append(c.events, xtestEvent{eventType: eventType, detail: detail})
	first := len(c.events) == 1
	c.mu.Unlock()
	if first {
		c.firstOnce()
		<-c.firstRelease
	}
	return x11.NewMockXTestFakeInputCookie(nil)
}

func (c *orderingXTestConnection) Close() {
	c.mu.Lock()
	c.closeCalls++
	c.mu.Unlock()
}

func (c *orderingXTestConnection) eventsSnapshot() []xtestEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]xtestEvent(nil), c.events...)
}

func (c *orderingXTestConnection) closeCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCalls
}

func newTestXTestBackend(
	t *testing.T,
	keysymsPerKeycode byte,
	keysyms []xproto.Keysym,
	fail func(xtestEvent) error,
) (*XTestBackend, *[]xtestEvent) {
	t.Helper()
	if keysymsPerKeycode == 0 || len(keysyms)%int(keysymsPerKeycode) != 0 {
		t.Fatalf("invalid test keymap: %d keysyms per keycode, %d keysyms", keysymsPerKeycode, len(keysyms))
	}

	events := make([]xtestEvent, 0, 8)
	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{
				MinKeycode: 8,
				MaxKeycode: 8 + xproto.Keycode(len(keysyms)/int(keysymsPerKeycode)) - 1,
			}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: keysymsPerKeycode,
				Keysyms:           keysyms,
			})
		},
		FakeInputCheckedFunc: func(eventType byte, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
			event := xtestEvent{eventType: eventType, detail: detail}
			events = append(events, event)
			if fail != nil {
				if err := fail(event); err != nil {
					return x11.NewMockXTestFakeInputCookie(err)
				}
			}
			return x11.NewMockXTestFakeInputCookie(nil)
		},
	}
	return &XTestBackend{conn: conn, root: 1}, &events
}

func TestXTestTypeReleasesTemporaryModifiersAfterKeyFailure(t *testing.T) {
	const (
		shift = byte(8)
		ctrl  = byte(9)
		key   = byte(12)
	)
	wantErr := errors.New("synthetic key failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "{ctrl+shift+a}")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: key},
		{eventType: xproto.KeyRelease, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestMouseMoveRejectsCoordinatesThatWouldTruncate(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{name: "x below", x: -1<<15 - 1, y: 0},
		{name: "x above", x: 1<<15 + 0, y: 0},
		{name: "y below", x: 0, y: -1<<15 - 1},
		{name: "y above", x: 0, y: 1<<15 + 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
			if err := b.MouseMove(context.Background(), tc.x, tc.y); err == nil {
				t.Fatal("MouseMove accepted a coordinate outside the X11 int16 range")
			}
			if len(*events) != 0 {
				t.Fatalf("events = %#v, want no wire event after validation failure", *events)
			}
		})
	}
}

func TestXTestTypeJoinsTemporaryModifierCleanupFailure(t *testing.T) {
	const (
		shift = byte(8)
		ctrl  = byte(9)
		key   = byte(12)
	)
	keyErr := errors.New("synthetic key failure")
	cleanupErr := errors.New("synthetic modifier cleanup failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			return keyErr
		}
		if event.eventType == xproto.KeyRelease && event.detail == ctrl {
			return cleanupErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "{ctrl+shift+a}")
	if !errors.Is(err, keyErr) {
		t.Fatalf("typeContext error = %v, want key error %v", err, keyErr)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("typeContext error = %v, want cleanup error %v joined", err, cleanupErr)
	}
	if got := *events; len(got) != 6 || got[3] != (xtestEvent{eventType: xproto.KeyRelease, detail: key}) || got[4] != (xtestEvent{eventType: xproto.KeyRelease, detail: ctrl}) {
		t.Fatalf("events = %#v, want uncertain key release before modifier cleanup", got)
	}
}

func TestXTestTypeReleasesEarlierModifiersWhenModifierSetupFails(t *testing.T) {
	const (
		ctrl  = byte(9)
		shift = byte(8)
	)
	wantErr := errors.New("synthetic modifier failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == ctrl {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "{ctrl+shift+a}")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestTypeTextReleasesShiftAfterKeyFailure(t *testing.T) {
	const (
		shift = byte(8)
		key   = byte(9)
	)
	wantErr := errors.New("synthetic text key failure")
	b, events := newTestXTestBackend(t, 2, []xproto.Keysym{
		0xffe1, 0,
		0x61, 0x41,
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "A")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: key},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestTypeTextJoinsShiftCleanupFailure(t *testing.T) {
	const (
		shift = byte(8)
		key   = byte(9)
	)
	keyErr := errors.New("synthetic text key failure")
	cleanupErr := errors.New("synthetic shift cleanup failure")
	b, _ := newTestXTestBackend(t, 2, []xproto.Keysym{
		0xffe1, 0,
		0x61, 0x41,
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			return keyErr
		}
		if event.eventType == xproto.KeyRelease && event.detail == shift {
			return cleanupErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "A")
	if !errors.Is(err, keyErr) {
		t.Fatalf("typeContext error = %v, want key error %v", err, keyErr)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("typeContext error = %v, want cleanup error %v joined", err, cleanupErr)
	}
}

func TestXTestTypeReleasesTemporaryModifiersAfterCancellation(t *testing.T) {
	const (
		shift = byte(8)
		ctrl  = byte(9)
		key   = byte(12)
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			cancel()
		}
		return nil
	})
	b.delay = 10 * time.Millisecond

	err := b.typeContext(ctx, "{ctrl+shift+a}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("typeContext error = %v, want context.Canceled", err)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: key},
		{eventType: xproto.KeyRelease, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func sameXTestEvents(got, want []xtestEvent) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type xtestOperationOrderingCase struct {
	name       string
	newBackend func() (*XTestBackend, *orderingXTestConnection)
	first      func(*XTestBackend) error
	second     func(*XTestBackend) error
	want       []xtestEvent
}

func TestXTestLogicalOperationsDoNotInterleave(t *testing.T) {
	tests := []xtestOperationOrderingCase{
		{
			name: "Type",
			newBackend: func() (*XTestBackend, *orderingXTestConnection) {
				conn := newOrderingXTestConnection([]xproto.Keysym{0x61, 0x62, 0x63})
				return &XTestBackend{conn: conn, root: 1, delay: 0}, conn
			},
			first:  func(b *XTestBackend) error { return b.Type(context.Background(), "ab") },
			second: func(b *XTestBackend) error { return b.Type(context.Background(), "c") },
			want: []xtestEvent{
				{eventType: xproto.KeyPress, detail: 8},
				{eventType: xproto.KeyRelease, detail: 8},
				{eventType: xproto.KeyPress, detail: 9},
				{eventType: xproto.KeyRelease, detail: 9},
				{eventType: xproto.KeyPress, detail: 10},
				{eventType: xproto.KeyRelease, detail: 10},
			},
		},
		{
			name: "MouseClick",
			newBackend: func() (*XTestBackend, *orderingXTestConnection) {
				conn := newOrderingXTestConnection(nil)
				return &XTestBackend{conn: conn, root: 1, delay: 0}, conn
			},
			first: func(b *XTestBackend) error {
				return b.MouseClick(context.Background(), 1, 2, 1)
			},
			second: func(b *XTestBackend) error {
				return b.MouseClick(context.Background(), 3, 4, 2)
			},
			want: []xtestEvent{
				{eventType: xproto.MotionNotify, detail: 0},
				{eventType: xproto.ButtonPress, detail: 1},
				{eventType: xproto.ButtonRelease, detail: 1},
				{eventType: xproto.MotionNotify, detail: 0},
				{eventType: xproto.ButtonPress, detail: 2},
				{eventType: xproto.ButtonRelease, detail: 2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, conn := tt.newBackend()
			assertXTestOperationsDoNotInterleave(t, b, conn, tt.first, tt.second, tt.want)
		})
	}
}

func assertXTestOperationsDoNotInterleave(
	t *testing.T,
	b *XTestBackend,
	conn *orderingXTestConnection,
	first func(*XTestBackend) error,
	second func(*XTestBackend) error,
	want []xtestEvent,
) {
	t.Helper()
	firstDone := make(chan error, 1)
	go func() { firstDone <- first(b) }()
	select {
	case <-conn.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first operation did not reach the connection")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- second(b) }()
	select {
	case err := <-secondDone:
		t.Fatalf("second operation completed while first was blocked: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := len(conn.eventsSnapshot()); got != 1 {
		t.Fatalf("events while first operation was blocked = %d, want 1", got)
	}

	close(conn.firstRelease)
	for name, done := range map[string]<-chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s operation: %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s operation did not finish", name)
		}
	}
	if got := conn.eventsSnapshot(); !sameXTestEvents(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestXTestQueuedOperationHonorsCancellation(t *testing.T) {
	conn := newOrderingXTestConnection([]xproto.Keysym{0x61, 0x62})
	b := &XTestBackend{conn: conn, root: 1, delay: 0}

	firstDone := make(chan error, 1)
	go func() { firstDone <- b.Type(context.Background(), "a") }()
	select {
	case <-conn.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first operation did not reach the connection")
	}

	queuedCtx, cancel := context.WithCancel(context.Background())
	queuedDone := make(chan error, 1)
	go func() { queuedDone <- b.Type(queuedCtx, "b") }()
	cancel()
	select {
	case err := <-queuedDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued operation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued operation did not honor cancellation")
	}

	close(conn.firstRelease)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first operation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first operation did not finish")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestXTestCloseWaitsForActiveOperation(t *testing.T) {
	conn := newOrderingXTestConnection([]xproto.Keysym{0x61})
	b := &XTestBackend{conn: conn, root: 1, delay: time.Second}

	opDone := make(chan error, 1)
	go func() { opDone <- b.Type(context.Background(), "a") }()
	select {
	case <-conn.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("operation did not reach the connection")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while operation was active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := conn.closeCallCount(); got != 0 {
		t.Fatalf("connection close calls while operation was active = %d, want 0", got)
	}

	close(conn.firstRelease)
	select {
	case err := <-opDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("operation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active operation did not finish after release")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after active operation ended")
	}
	if got := conn.closeCallCount(); got != 1 {
		t.Fatalf("connection close calls = %d, want 1", got)
	}
}

func TestXTestRejectsNewWorkAfterClose(t *testing.T) {
	conn := newOrderingXTestConnection([]xproto.Keysym{0x61})
	b := &XTestBackend{conn: conn, root: 1, delay: 0}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := b.Type(context.Background(), "a"); !errors.Is(err, errXTestBackendClosed) {
		t.Fatalf("Type after Close = %v, want backend-closed error", err)
	}
	if err := b.MouseClick(context.Background(), 1, 2, 1); !errors.Is(err, errXTestBackendClosed) {
		t.Fatalf("MouseClick after Close = %v, want backend-closed error", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if got := conn.closeCallCount(); got != 1 {
		t.Fatalf("connection close calls after repeated Close = %d, want 1", got)
	}
}

// A layout may map either ISO_Level3_Shift or Mode_switch to the key that reaches
// its third level. They are distinct keysyms, so looking for only the first
// reported a layout that does have AltGr as having none.
func TestXTestAltGrAcceptsEitherThirdLevelKeysym(t *testing.T) {
	const (
		keysymsPerKey = 4
		charKeycode   = 10
		altGrKeycode  = 11
	)

	for _, testCase := range []struct {
		name     string
		altGrSym xproto.Keysym
	}{
		{"ISO_Level3_Shift", 0xfe03},
		{"Mode_switch", 0xff7e},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			keysyms := []xproto.Keysym{
				0, 0, 0, 0, // keycode 8: unused
				0xffe1, 0, 0, 0, // keycode 9: left shift
				0x61, 0x41, 0x20ac, 0x20ac, // keycode 10: a, A, Euro
				testCase.altGrSym, 0, 0, 0, // keycode 11: the AltGr key
			}

			b, _ := newTestXTestBackend(t, keysymsPerKey, keysyms, nil)

			kc, err := b.altGrKeycode()
			if err != nil {
				t.Fatalf("altGrKeycode: %v", err)
			}
			if kc != altGrKeycode {
				t.Fatalf("altGrKeycode = %d, want %d", kc, altGrKeycode)
			}

			// The Euro sign sits at the third level, so reaching it needs AltGr.
			required, err := b.levelModifiers(2)
			if err != nil {
				t.Fatalf("levelModifiers(2): %v", err)
			}
			if len(required) != 1 || required[0] != altGrKeycode {
				t.Fatalf("levelModifiers(2) = %v, want just the AltGr keycode %d", required, altGrKeycode)
			}
			gotKeycode, level, err := b.keycodeAndLevel(0x20ac)
			if err != nil {
				t.Fatalf("Euro sign not reachable: %v", err)
			}
			if gotKeycode != charKeycode || level != 2 {
				t.Fatalf("Euro sign at keycode %d level %d, want keycode %d level 2", gotKeycode, level, charKeycode)
			}
		})
	}
}

// A layout that maps neither keysym genuinely has no reachable third level.
func TestXTestAltGrAbsentReportsBothKeysyms(t *testing.T) {
	keysyms := []xproto.Keysym{
		0, 0, 0, 0, // keycode 8: unused
		0xffe1, 0, 0, 0, // keycode 9: left shift
		0x61, 0x41, 0x20ac, 0x20ac, // keycode 10: a, A, Euro
		0xffe3, 0, 0, 0, // keycode 11: plain right Alt, not a third-level switch
	}
	b, _ := newTestXTestBackend(t, 4, keysyms, nil)

	if _, err := b.altGrKeycode(); err == nil {
		t.Fatal("altGrKeycode succeeded on a layout with no third-level switch")
	} else if !strings.Contains(err.Error(), "ISO_Level3_Shift") || !strings.Contains(err.Error(), "Mode_switch") {
		t.Fatalf("error = %v, want it to name both keysyms it looked for", err)
	}
}

// A map may report more slots per keycode than the four this backend can name.
// Naming a modifier for an unknown slot would guess, so the level is reported as
// unsupported, and the message says what is actually missing rather than claiming
// a limit X11 does not impose.
func TestXTestWideMapReportsUnsupportedLevel(t *testing.T) {
	const keysymsPerKey = 7

	keysyms := []xproto.Keysym{
		0, 0, 0, 0, 0, 0, 0, // keycode 8: unused
		0xffe1, 0, 0, 0, 0, 0, 0, // keycode 9: left shift
		0x61, 0x41, 0x20ac, 0x20ac, 0, 0, 0, // keycode 10: a, A, Euro
		0xfe03, 0, 0, 0, 0, 0, 0, // keycode 11: ISO_Level3_Shift
	}

	b, _ := newTestXTestBackend(t, keysymsPerKey, keysyms, nil)

	kc, level, err := b.keycodeAndLevel(0x20ac)
	if err != nil {
		t.Fatalf("Euro sign not reachable: %v", err)
	}
	if kc != 10 || level != 2 {
		t.Fatalf("Euro sign at keycode %d level %d, want keycode 10 level 2", kc, level)
	}
	// A level this backend can name stays expressible on a wider map.
	if required, err := b.levelModifiers(level); err != nil || len(required) != 1 {
		t.Fatalf("levelModifiers(%d) = %v, err=%v; want one AltGr keycode", level, required, err)
	}

	// A level past what this backend can name is refused, and says why.
	if _, err := b.levelModifiers(5); err == nil {
		t.Fatal("levelModifiers(5) succeeded, want an unsupported-level error")
	} else if !strings.Contains(err.Error(), "keyboard-group") {
		t.Fatalf("unsupported-level error = %v, want it to name the missing keyboard-group information", err)
	}
}

// The keymap is re-read after a failed read instead of the failure being cached.
// Caching it left the backend unable to name any keycode for the rest of the
// process, so every keystroke failed and no held modifier was ever recognised.
func TestXTestRetriesTheKeymapAfterAFailedRead(t *testing.T) {
	reads := 0
	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 8 + 3 - 1}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			reads++
			if reads == 1 {
				return x11.NewMockGetKeyboardMappingErrorCookie(errTestKeymapRead)
			}
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: 4,
				Keysyms:           []xproto.Keysym{0xffe1, 0, 0, 0, 0x61, 0x41, 0, 0},
			})
		},
	}
	b := &XTestBackend{conn: conn}

	// The modifier set is the thing that must not be cached here: an empty one makes
	// every held modifier invisible, so a caller's Ctrl is pressed again on every
	// character and released after each one. The first read fails, so this call has
	// nothing to build from and must leave nothing behind.
	if got := b.modifierCodeSet(); got != nil {
		t.Fatalf("modifier set = %v after a failed keymap read, want nil so the next call retries", got)
	}

	set := b.modifierCodeSet()
	if set == nil {
		t.Fatal("modifier set still nil after the keymap became readable")
	}
	if len(set) == 0 {
		t.Fatal("modifier set is empty on a layout that maps a shift key")
	}
	if reads != 2 {
		t.Fatalf("GetKeyboardMapping calls = %d, want 2", reads)
	}
}

// The modifier set is only unknowable when the keymap could not be read. A layout
// that genuinely maps no key for one modifier still gets a usable set.
func TestXTestModifierSetCachesWhenOnlyOneModifierIsAbsent(t *testing.T) {
	b, _ := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)

	first := b.modifierCodeSet()
	if first == nil {
		t.Fatal("modifier set was not built on a usable layout")
	}
	second := b.modifierCodeSet()
	if len(first) == 0 {
		t.Fatal("modifier set is empty on a layout that maps modifiers")
	}
	// Cached: the same set comes back without re-reading.
	if len(second) != len(first) {
		t.Fatalf("cached modifier set changed size: %d then %d", len(first), len(second))
	}
}

var errTestKeymapRead = errors.New("test keymap read failure")

// A keysym can appear at several levels and keycodes. Keeping whichever came first
// reported a character as untypable when its first occurrence sits at a level this
// backend cannot express, while a later one is directly typable.
func TestEnsureKeymapPrefersATypableLevelForARepeatedKeysym(t *testing.T) {
	// Nine keysyms per keycode, so levels run past the four levelModifiers can name.
	// The same keysym sits at level 4 on the first keycode and level 0 on the second:
	// iteration reaches the untypable one first.
	const kpk = 9
	keysyms := make([]xproto.Keysym, kpk*2)
	keysyms[4] = 'a' // first keycode, level 4: not expressible
	keysyms[kpk+0] = 'a'

	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 9}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: kpk,
				Keysyms:           keysyms,
			})
		},
	}
	b := &XTestBackend{conn: conn}

	mapping, err := b.ensureKeymap()
	if err != nil {
		t.Fatalf("ensureKeymap: %v", err)
	}
	got, ok := mapping['a']
	if !ok {
		t.Fatal("the repeated keysym is missing from the mapping")
	}
	if got.level > maxTypableLevel {
		t.Fatalf("keysym 'a' resolved to level %d, which cannot be expressed; a typable occurrence existed", got.level)
	}
	if _, err := b.levelModifiers(got.level); err != nil {
		t.Fatalf("the resolved level %d is not expressible: %v", got.level, err)
	}
	if _, _, err := b.keycodeAndLevel('a'); err != nil {
		t.Fatalf("the keysym is reported untypable: %v", err)
	}
}
