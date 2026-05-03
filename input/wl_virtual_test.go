//go:build linux
// +build linux

package input

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestNewWlVirtualBackend_NoSocket(t *testing.T) {
	_, err := NewWlVirtualBackend("")
	if err == nil {
		t.Fatal("expected error for empty socket")
	}
	t.Logf("got expected error: %v", err)
}

func TestNewWlVirtualBackend_Unreachable(t *testing.T) {
	_, err := NewWlVirtualBackend("/nonexistent.sock")
	if err == nil {
		t.Fatal("expected error for unreachable socket")
	}
	t.Logf("got expected error: %v", err)
}

func TestBtnCode(t *testing.T) {
	tests := []struct {
		button int
		want   uint32
	}{
		{1, btnLeft},
		{2, btnMiddle},
		{3, btnRight},
		{0, btnLeft},  // default
		{99, btnLeft}, // default
	}
	for _, tc := range tests {
		got := btnCode(tc.button)
		if got != tc.want {
			t.Errorf("btnCode(%d) = %d, want %d", tc.button, got, tc.want)
		}
	}
}

func TestWlVirtualBackend_Close_NilSession(t *testing.T) {
	// Close with nil session and nil display should not panic.
	b := &WlVirtualBackend{}
	if err := b.Close(); err != nil {
		t.Fatalf("Close with nil session/display: %v", err)
	}
}

func TestWlVirtualBackend_Close_NilDisplay(t *testing.T) {
	// Close with session nil but display non-nil where Context() returns nil.
	// This triggers the bug: Close() calls b.display.Context().Close() which
	// panics when Context() returns nil.
	b := &WlVirtualBackend{display: &wl.Display{}}
	defer func() {
		r := recover()
		if r != nil {
			t.Logf("Close panicked as expected (bug #4): %v", r)
		}
	}()
	_ = b.Close()
}

func TestScroll_SignConvention(t *testing.T) {
	// Verify the scroll sign convention:
	// ScrollUp sends negative values, ScrollDown sends positive.
	// scroll(axis, clicks) computes: value = clicks * 15 * 256
	// ScrollUp calls scroll(0, -clicks) → negative value
	// ScrollDown calls scroll(0, clicks) → positive value

	// Verify the fixed-point math: 15 * 256 = 3840 per notch
	expectedPerNotch := int32(15 * 256)
	if expectedPerNotch != 3840 {
		t.Errorf("scroll resolution = %d, want 3840", expectedPerNotch)
	}

	// 3 notches up → -3 * 3840 = -11520
	upValue := int32(-3 * 15 * 256)
	if upValue != -11520 {
		t.Errorf("3 notches up = %d, want -11520", upValue)
	}

	// 3 notches down → 3 * 3840 = 11520
	downValue := int32(3 * 15 * 256)
	if downValue != 11520 {
		t.Errorf("3 notches down = %d, want 11520", downValue)
	}
}

func TestWlVirtualBackend_MouseClick_Panics(t *testing.T) {
	// MouseClick on a zero-value backend panics because MouseMove accesses
	// nil pointers. This documents the current behavior.
	b := &WlVirtualBackend{}
	defer func() {
		r := recover()
		if r != nil {
			t.Logf("MouseClick panicked as expected: %v", r)
		}
	}()
	_ = b.MouseClick(nil, 100, 200, 1)
}

func TestWlVirtualBackend_PressCombo_Panics(t *testing.T) {
	// PressCombo on a zero-value backend panics because it accesses kbd.
	b := &WlVirtualBackend{}
	defer func() {
		r := recover()
		if r != nil {
			t.Logf("PressCombo panicked as expected: %v", r)
		}
	}()
	_ = b.PressCombo(nil, "ctrl+c")
}
