package input

import (
	"context"
	"errors"
	"testing"
)

// Portal permissions are granular. If a session authorizes pointer motion
// but not button injection, a requested click must fail before moving the
// pointer or altering any held-input state.
func TestRemoteDesktopClickWithoutButtonCapabilityNeverMovesPointer(t *testing.T) {
	backend, _, _ := newSyntheticRevocableRemoteDesktop(t)
	backend.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	backend.sender.button = 0
	if backend.hasPointer {
		t.Fatal("synthetic pointer unexpectedly initialized")
	}
	err := backend.MouseClick(context.Background(), 25, 30, 1)
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("click lacking EIS button device returned %v", err)
	}
	if backend.hasPointer || backend.pointerX != 0 || backend.pointerY != 0 || len(backend.buttons) != 0 {
		t.Fatalf("partial click moved pointer or held button: moved=%t pos=(%d,%d) buttons=%v", backend.hasPointer, backend.pointerX, backend.pointerY, backend.buttons)
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("local permission rejection retired healthy portal: %v", err)
	}
}

func TestRemoteDesktopMissingEISObjectsFailClosedWithoutChangingInputState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		disable func(*eiSender)
		op      func(context.Context, *RemoteDesktopBackend) error
	}{
		{"keyboard object down", func(s *eiSender) { s.keyboard = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.KeyDown(ctx, "a") }},
		{"keyboard device up", func(s *eiSender) { s.keyboardDev = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.KeyUp(ctx, "a") }},
		{"button object down", func(s *eiSender) { s.button = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.MouseDown(ctx, 1) }},
		{"button device up", func(s *eiSender) { s.buttonDev = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.MouseUp(ctx, 1) }},
		{"pointer object move", func(s *eiSender) { s.pointers[0].pointer = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.MouseMove(ctx, 30, 40) }},
		{"pointer device move", func(s *eiSender) { s.pointers[0].device = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.MouseMove(ctx, 30, 40) }},
		{"missing key during type", func(s *eiSender) { s.keyboard = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.Type(ctx, "first {enter}") }},
		{"missing key device during type", func(s *eiSender) { s.keyboardDev = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.Type(ctx, "first {enter}") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
			tc.disable(b.sender)
			if err := tc.op(context.Background(), b); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("missing EIS capability accepted: %v", err)
			}
			if b.hasPointer || len(b.heldKeys) > 0 || len(b.buttons) > 0 {
				t.Fatalf("missing EIS device recorded partial action: pointer=%t keys=%v buttons=%v", b.hasPointer, b.heldKeys, b.buttons)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("missing EIS capability revoked session instead of returning local validation: %v", err)
			}
		})
	}
}
