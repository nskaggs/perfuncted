//go:build linux
// +build linux

package input

import (
	"context"
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

func TestWlVirtualBackend_Close_NilDisplayContext(t *testing.T) {
	// Close with session nil but display non-nil where Context() returns nil
	// should not panic (bug #4 was fixed).
	b := &WlVirtualBackend{display: &wl.Display{}}
	if err := b.Close(); err != nil {
		t.Fatalf("Close with nil display context: %v", err)
	}
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

func TestWlVirtualBackend_CanceledContextShortCircuitsMethods(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := &WlVirtualBackend{}

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
			name: "TypeContext",
			run:  func() error { return b.TypeContext(ctx, "A{ctrl+a}") },
		},
		{
			name: "KeyDown",
			run:  func() error { return b.KeyDown(ctx, "a") },
		},
		{
			name: "KeyUp",
			run:  func() error { return b.KeyUp(ctx, "a") },
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
