//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/bendahl/uinput"
)

type recordingKeyboard struct {
	events   []string
	closeErr error
}

func (k *recordingKeyboard) KeyPress(key int) error {
	k.events = append(k.events, fmt.Sprintf("press:%d", key))
	return nil
}

func (k *recordingKeyboard) KeyDown(key int) error {
	k.events = append(k.events, fmt.Sprintf("down:%d", key))
	return nil
}

func (k *recordingKeyboard) KeyUp(key int) error {
	k.events = append(k.events, fmt.Sprintf("up:%d", key))
	return nil
}

func (k *recordingKeyboard) FetchSyspath() (string, error) { return "", nil }

func (k *recordingKeyboard) Close() error { return k.closeErr }

var _ uinput.Keyboard = (*recordingKeyboard)(nil)

type recordingTouchPad struct {
	events   []string
	closeErr error
}

func (t *recordingTouchPad) record(s string) { t.events = append(t.events, s) }
func (t *recordingTouchPad) MoveTo(x int32, y int32) error {
	t.record(fmt.Sprintf("move:%d,%d", x, y))
	return nil
}
func (t *recordingTouchPad) LeftClick() error  { t.record("left-click"); return nil }
func (t *recordingTouchPad) RightClick() error { t.record("right-click"); return nil }
func (t *recordingTouchPad) LeftPress() error  { t.record("left-press"); return nil }
func (t *recordingTouchPad) LeftRelease() error {
	t.record("left-release")
	return nil
}
func (t *recordingTouchPad) RightPress() error { t.record("right-press"); return nil }
func (t *recordingTouchPad) RightRelease() error {
	t.record("right-release")
	return nil
}
func (t *recordingTouchPad) TouchDown() error              { t.record("touch-down"); return nil }
func (t *recordingTouchPad) TouchUp() error                { t.record("touch-up"); return nil }
func (t *recordingTouchPad) FetchSyspath() (string, error) { return "", nil }
func (t *recordingTouchPad) Close() error                  { t.record("close"); return t.closeErr }

var _ uinput.TouchPad = (*recordingTouchPad)(nil)

type recordingMouse struct {
	events   []string
	closeErr error
}

func (m *recordingMouse) record(s string) { m.events = append(m.events, s) }
func (m *recordingMouse) MoveLeft(pixel int32) error {
	m.record(fmt.Sprintf("move-left:%d", pixel))
	return nil
}
func (m *recordingMouse) MoveRight(pixel int32) error {
	m.record(fmt.Sprintf("move-right:%d", pixel))
	return nil
}
func (m *recordingMouse) MoveUp(pixel int32) error {
	m.record(fmt.Sprintf("move-up:%d", pixel))
	return nil
}
func (m *recordingMouse) MoveDown(pixel int32) error {
	m.record(fmt.Sprintf("move-down:%d", pixel))
	return nil
}
func (m *recordingMouse) Move(x, y int32) error {
	m.record(fmt.Sprintf("move:%d,%d", x, y))
	return nil
}
func (m *recordingMouse) LeftClick() error     { m.record("left-click"); return nil }
func (m *recordingMouse) RightClick() error    { m.record("right-click"); return nil }
func (m *recordingMouse) MiddleClick() error   { m.record("middle-click"); return nil }
func (m *recordingMouse) LeftPress() error     { m.record("left-press"); return nil }
func (m *recordingMouse) LeftRelease() error   { m.record("left-release"); return nil }
func (m *recordingMouse) RightPress() error    { m.record("right-press"); return nil }
func (m *recordingMouse) RightRelease() error  { m.record("right-release"); return nil }
func (m *recordingMouse) MiddlePress() error   { m.record("middle-press"); return nil }
func (m *recordingMouse) MiddleRelease() error { m.record("middle-release"); return nil }
func (m *recordingMouse) Wheel(horizontal bool, delta int32) error {
	m.record(fmt.Sprintf("wheel:%t:%d", horizontal, delta))
	return nil
}
func (m *recordingMouse) FetchSyspath() (string, error) { return "", nil }
func (m *recordingMouse) FetchSysPath() (string, error) { return "", nil }
func (m *recordingMouse) Close() error                  { m.record("close"); return m.closeErr }

var _ uinput.Mouse = (*recordingMouse)(nil)

// newTestBackend creates a UinputBackend with a recording keyboard and the
// QWERTY fallback rune map for tests that don't depend on kernel keymap probing.
func newTestBackend(t *testing.T) (*UinputBackend, *recordingKeyboard) {
	t.Helper()
	kb := &recordingKeyboard{}
	return &UinputBackend{
		kb:         kb,
		charToRune: qwertyRuneMap(),
	}, kb
}

func newUinputActionBackend() (*UinputBackend, *recordingKeyboard, *recordingTouchPad, *recordingMouse) {
	kb := &recordingKeyboard{}
	tp := &recordingTouchPad{}
	mouse := &recordingMouse{}
	return &UinputBackend{
		kb:         kb,
		touchpad:   tp,
		mouse:      mouse,
		charToRune: qwertyRuneMap(),
	}, kb, tp, mouse
}

func TestUinputKeyDownAndUp(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.KeyDown(context.Background(), "a"); err != nil {
		t.Fatalf("KeyDown: %v", err)
	}
	if err := b.KeyUp(context.Background(), "a"); err != nil {
		t.Fatalf("KeyUp: %v", err)
	}

	want := []string{fmt.Sprintf("down:%d", uinput.KeyA), fmt.Sprintf("up:%d", uinput.KeyA)}
	if len(kb.events) != len(want) {
		t.Fatalf("events = %v, want %v", kb.events, want)
	}
	for i, exp := range want {
		if kb.events[i] != exp {
			t.Fatalf("event %d = %q, want %q", i, kb.events[i], exp)
		}
	}
}

func TestUinputTypeContextUppercaseUsesShift(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.Type(context.Background(), "A"); err != nil {
		t.Fatalf("Type: %v", err)
	}

	want := []string{
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.KeyA),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextLowercaseDoesNotUseShift(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.TypeContext(context.Background(), "a"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{fmt.Sprintf("press:%d", uinput.KeyA)}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextSymbolUsesShift(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.TypeContext(context.Background(), "!"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.Key1),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextMixedText(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.TypeContext(context.Background(), "Hi!"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{
		// H: shift + h
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.KeyH),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
		// i: no shift
		fmt.Sprintf("press:%d", uinput.KeyI),
		// !: shift + 1
		fmt.Sprintf("down:%d", uinput.KeyLeftshift),
		fmt.Sprintf("press:%d", uinput.Key1),
		fmt.Sprintf("up:%d", uinput.KeyLeftshift),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputTypeContextUnsupportedChar(t *testing.T) {
	b, kb := newTestBackend(t)

	// Euro sign is not in the QWERTY fallback map
	err := b.TypeContext(context.Background(), "€")
	if err == nil {
		t.Fatal("expected error for unsupported character, got nil")
	}
	if kb.events != nil {
		t.Fatalf("expected no events for unsupported character, got %v", kb.events)
	}
}

func TestUinputTypeContextWithKeyCombo(t *testing.T) {
	b, kb := newTestBackend(t)

	// {ctrl+a} should use resolveKey path, not charToRune
	if err := b.TypeContext(context.Background(), "{ctrl+a}"); err != nil {
		t.Fatalf("TypeContext: %v", err)
	}

	want := []string{
		// ctrl down
		fmt.Sprintf("down:%d", uinput.KeyLeftctrl),
		// a press
		fmt.Sprintf("press:%d", uinput.KeyA),
		// ctrl up
		fmt.Sprintf("up:%d", uinput.KeyLeftctrl),
	}
	if len(kb.events) != len(want) {
		t.Fatalf("unexpected events: got %v want %v", kb.events, want)
	}
	for i, event := range want {
		if kb.events[i] != event {
			t.Fatalf("event %d = %q, want %q (all events: %v)", i, kb.events[i], event, kb.events)
		}
	}
}

func TestUinputMouseActionsAndScroll(t *testing.T) {
	b, _, tp, mouse := newUinputActionBackend()

	t.Run("MouseMove", func(t *testing.T) {
		tp.events = nil
		if err := b.MouseMove(context.Background(), 11, 22); err != nil {
			t.Fatalf("MouseMove: %v", err)
		}
		want := []string{"move:11,22"}
		if len(tp.events) != len(want) {
			t.Fatalf("events = %v, want %v", tp.events, want)
		}
		for i, exp := range want {
			if tp.events[i] != exp {
				t.Fatalf("event %d = %q, want %q", i, tp.events[i], exp)
			}
		}
	})

	t.Run("MouseClick", func(t *testing.T) {
		tp.events = nil
		if err := b.MouseClick(context.Background(), 3, 4, 1); err != nil {
			t.Fatalf("MouseClick: %v", err)
		}
		want := []string{"move:3,4", "left-press", "left-release"}
		if len(tp.events) != len(want) {
			t.Fatalf("events = %v, want %v", tp.events, want)
		}
		for i, exp := range want {
			if tp.events[i] != exp {
				t.Fatalf("event %d = %q, want %q", i, tp.events[i], exp)
			}
		}
	})

	t.Run("MouseButtons", func(t *testing.T) {
		tp.events = nil
		mouse.events = nil

		if err := b.MouseDown(context.Background(), 1); err != nil {
			t.Fatalf("MouseDown left: %v", err)
		}
		if err := b.MouseUp(context.Background(), 3); err != nil {
			t.Fatalf("MouseUp right: %v", err)
		}
		if err := b.MouseDown(context.Background(), 2); err != nil {
			t.Fatalf("MouseDown middle: %v", err)
		}
		if err := b.MouseUp(context.Background(), 2); err != nil {
			t.Fatalf("MouseUp middle: %v", err)
		}

		if got := tp.events; len(got) != 2 || got[0] != "left-press" || got[1] != "right-release" {
			t.Fatalf("touchpad events = %v, want [left-press right-release]", got)
		}
		if got := mouse.events; len(got) != 2 || got[0] != "middle-press" || got[1] != "middle-release" {
			t.Fatalf("mouse events = %v, want [middle-press middle-release]", got)
		}
	})

	t.Run("Scroll", func(t *testing.T) {
		mouse.events = nil
		if err := b.ScrollUp(context.Background(), 2); err != nil {
			t.Fatalf("ScrollUp: %v", err)
		}
		if err := b.ScrollRight(context.Background(), 3); err != nil {
			t.Fatalf("ScrollRight: %v", err)
		}
		if len(mouse.events) != 2 {
			t.Fatalf("mouse events = %v, want 2 entries", mouse.events)
		}
		if mouse.events[0] != "wheel:false:-2" {
			t.Fatalf("ScrollUp event = %q, want wheel:false:-2", mouse.events[0])
		}
		if mouse.events[1] != "wheel:true:3" {
			t.Fatalf("ScrollRight event = %q, want wheel:true:3", mouse.events[1])
		}
	})
}

func TestUinputPointerLocationUnsupported(t *testing.T) {
	b, _ := newTestBackend(t)
	if _, _, err := b.PointerLocation(context.Background()); err == nil {
		t.Fatal("PointerLocation succeeded unexpectedly")
	}
}

func TestUinputCloseClosesAllDevices(t *testing.T) {
	kb := &recordingKeyboard{closeErr: errors.New("keyboard close failed")}
	tp := &recordingTouchPad{closeErr: errors.New("touchpad close failed")}
	mouse := &recordingMouse{closeErr: errors.New("mouse close failed")}
	b := &UinputBackend{
		kb:         kb,
		touchpad:   tp,
		mouse:      mouse,
		charToRune: qwertyRuneMap(),
	}

	err := b.Close()
	if !errors.Is(err, kb.closeErr) || !errors.Is(err, tp.closeErr) || !errors.Is(err, mouse.closeErr) {
		t.Fatalf("Close error = %v, want joined close errors", err)
	}
	if len(kb.events) != 0 || len(tp.events) != 1 || len(mouse.events) != 1 {
		t.Fatalf("close events = kb:%v tp:%v mouse:%v", kb.events, tp.events, mouse.events)
	}
	if tp.events[0] != "close" || mouse.events[0] != "close" {
		t.Fatalf("close events = tp:%v mouse:%v, want close entries", tp.events, mouse.events)
	}
}

func TestQwertyRuneMapCompleteness(t *testing.T) {
	m := qwertyRuneMap()

	// All lowercase letters
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing lowercase %q", string(c))
		}
	}
	// All uppercase letters
	for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing uppercase %q", string(c))
		}
	}
	// All digits
	for _, c := range "0123456789" {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing digit %q", string(c))
		}
	}
	// Common symbols
	for _, c := range " !\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~" {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing symbol %q", string(c))
		}
	}
	// Whitespace
	for _, c := range []rune{' ', '\t', '\n'} {
		if _, ok := m[c]; !ok {
			t.Errorf("qwertyRuneMap missing whitespace %q", string(c))
		}
	}

	// Verify shift correctness for a few key entries
	if m['a'].shift {
		t.Error("lowercase 'a' should not require shift")
	}
	if !m['A'].shift {
		t.Error("uppercase 'A' should require shift")
	}
	if m['1'].shift {
		t.Error("digit '1' should not require shift")
	}
	if !m['!'].shift {
		t.Error("symbol '!' should require shift")
	}
	if m[' '].shift {
		t.Error("space should not require shift")
	}
}

func TestQwertyRuneMapKeycodesAreValid(t *testing.T) {
	m := qwertyRuneMap()

	// Every entry should have a non-zero keycode
	for r, kc := range m {
		if kc.keycode <= 0 {
			t.Errorf("rune %q has invalid keycode %d", string(r), kc.keycode)
		}
	}
}

func TestBuildKernelRuneMapFallback(t *testing.T) {
	// buildKernelRuneMap may or may not succeed depending on whether
	// /dev/tty0 is accessible in this environment. Either way, the
	// backend should still be usable via the QWERTY fallback.
	m, err := buildKernelRuneMap()
	if err != nil {
		// No accessible console — verify the fallback works
		m = qwertyRuneMap()
	}

	// The map (whichever source) should at minimum contain basic ASCII
	for _, c := range "abcdefghijklmnopqrstuvwxyz" {
		if _, ok := m[c]; !ok {
			t.Errorf("rune map missing lowercase %q (err=%v)", string(c), err)
		}
	}
	for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		if _, ok := m[c]; !ok {
			t.Errorf("rune map missing uppercase %q (err=%v)", string(c), err)
		}
	}
	for _, c := range "0123456789" {
		if _, ok := m[c]; !ok {
			t.Errorf("rune map missing digit %q (err=%v)", string(c), err)
		}
	}
}

func TestKernelRuneExtraction(t *testing.T) {
	tests := []struct {
		sym    uint16
		want   rune
		wantOK bool
	}{
		// KT_LATIN (type=0) with ASCII value
		{0x0061, 'a', true}, // KT_LATIN + 'a'
		{0x0041, 'A', true}, // KT_LATIN + 'A'
		{0x0030, '0', true}, // KT_LATIN + '0'
		{0x0020, ' ', true}, // KT_LATIN + space
		// KT_LETTER (type=11) with value
		{0x0B61, 'a', true}, // KT_LETTER + 'a'
		{0x0B41, 'A', true}, // KT_LETTER + 'A'
		// Non-Latin types should not extract
		{0x0100, 0, false}, // KT_FN
		{0x0200, 0, false}, // KT_SPEC
		{0x0300, 0, false}, // KT_PAD
		{0x0400, 0, false}, // KT_DEAD
		{0x0500, 0, false}, // KT_CONS
		{0x0600, 0, false}, // KT_CUR
		{0x0700, 0, false}, // KT_SHIFT
		{0x0800, 0, false}, // KT_META
		{0x0900, 0, false}, // KT_ASCII
		{0x0A00, 0, false}, // KT_LOCK
		// Zero value
		{0x0000, 0, true}, // KT_LATIN + 0 → valid extraction, rune 0
	}

	for _, tt := range tests {
		got, ok := kernelRune(tt.sym)
		if ok != tt.wantOK {
			t.Errorf("kernelRune(0x%04X) ok=%v, want %v", tt.sym, ok, tt.wantOK)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("kernelRune(0x%04X) = %q, want %q", tt.sym, string(got), string(tt.want))
		}
	}
}

// failingKeyboard wraps recordingKeyboard and fails on KeyPress calls.
type failingKeyboard struct {
	recordingKeyboard
	failPress bool
}

func (f *failingKeyboard) KeyPress(key int) error {
	if f.failPress {
		return fmt.Errorf("injected KeyPress failure for key %d", key)
	}
	return f.recordingKeyboard.KeyPress(key)
}

// TestTypeKeyWithMods_ModifierReleasedOnKeyPressFailure verifies that modifier
// keys are released (best-effort) when the key action itself fails. Before the
// fix, the pressed modifier was leaked (never released).
func TestTypeKeyWithMods_ModifierReleasedOnKeyPressFailure(t *testing.T) {
	kb := &failingKeyboard{failPress: true}
	b := &UinputBackend{kb: kb, charToRune: qwertyRuneMap()}

	// {shift+a}: presses shift (succeeds), then KeyPress('a') fails.
	// The fix should release shift even though KeyPress failed.
	err := b.TypeContext(context.Background(), "{shift+a}")
	if err == nil {
		t.Fatal("expected error from injected failure")
	}

	// Verify shift KeyDown was called.
	var downCount, upCount int
	for _, ev := range kb.events {
		if ev == fmt.Sprintf("down:%d", uinput.KeyLeftshift) {
			downCount++
		}
		if ev == fmt.Sprintf("up:%d", uinput.KeyLeftshift) {
			upCount++
		}
	}
	if downCount == 0 {
		t.Error("shift KeyDown was never called")
	}
	if upCount == 0 {
		t.Errorf("shift KeyUp was never called after failure; events: %v", kb.events)
	}
}

func TestUinputBackend_CanceledContextSuppressesKeyboardEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		run  func(*UinputBackend) error
	}{
		{
			name: "KeyDown",
			run:  func(b *UinputBackend) error { return b.KeyDown(ctx, "a") },
		},
		{
			name: "KeyUp",
			run:  func(b *UinputBackend) error { return b.KeyUp(ctx, "a") },
		},
		{
			name: "TypeContext",
			run:  func(b *UinputBackend) error { return b.TypeContext(ctx, "A{ctrl+a}") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, kb := newTestBackend(t)

			if err := tt.run(b); err != context.Canceled {
				t.Fatalf("%s canceled error = %v, want context.Canceled", tt.name, err)
			}
			if len(kb.events) != 0 {
				t.Fatalf("%s emitted events with canceled context: %v", tt.name, kb.events)
			}
		})
	}
}

func TestUinputBackend_CanceledContextShortCircuitsPointerMethods(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &UinputBackend{}

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "MouseMove",
			run:  func() error { return b.MouseMove(ctx, 1, 2) },
		},
		{
			name: "MouseClick",
			run:  func() error { return b.MouseClick(ctx, 1, 2, 1) },
		},
		{
			name: "MouseDown",
			run:  func() error { return b.MouseDown(ctx, 1) },
		},
		{
			name: "MouseUp",
			run:  func() error { return b.MouseUp(ctx, 1) },
		},
		{
			name: "ScrollUp",
			run:  func() error { return b.ScrollUp(ctx, 1) },
		},
		{
			name: "ScrollDown",
			run:  func() error { return b.ScrollDown(ctx, 1) },
		},
		{
			name: "ScrollLeft",
			run:  func() error { return b.ScrollLeft(ctx, 1) },
		},
		{
			name: "ScrollRight",
			run:  func() error { return b.ScrollRight(ctx, 1) },
		},
		{
			name: "Sync",
			run:  func() error { return b.Sync(ctx) },
		},
		{
			name: "PointerLocation",
			run: func() error {
				_, _, err := b.PointerLocation(ctx)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err != context.Canceled {
				t.Fatalf("%s canceled error = %v, want context.Canceled", tt.name, err)
			}
		})
	}
}
