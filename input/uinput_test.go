//go:build linux
// +build linux

package input

import (
	"context"
	"fmt"
	"testing"

	"github.com/bendahl/uinput"
)

type recordingKeyboard struct {
	events []string
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

func (k *recordingKeyboard) Close() error { return nil }

var _ uinput.Keyboard = (*recordingKeyboard)(nil)

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

func TestUinputTypeContextUppercaseUsesShift(t *testing.T) {
	b, kb := newTestBackend(t)

	if err := b.TypeContext(context.Background(), "A"); err != nil {
		t.Fatalf("TypeContext: %v", err)
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
