package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Exercise the actual serialized writer and connected Unix EIS socket. Each
// command must publish its state change only after frame dispatch succeeds.
func TestRemoteDesktopAuthorizedSyntheticEISCommandsAndHeldState(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 701, pointer: 702, regions: []eiRegion{{x: 10, y: 20, width: 200, height: 150, scale: 1}}}}
	ctx := context.Background()
	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatal(err)
	}
	ctrl, err := remoteKeyCode("ctrl")
	if err != nil {
		t.Fatal(err)
	}
	if !b.heldKeys[ctrl] {
		t.Fatal("acknowledged ctrl down did not record held key")
	}
	if err := b.Type(ctx, "{ctrl+a}"); err != nil {
		t.Fatalf("held modifier must survive combination: %v", err)
	}
	if !b.heldKeys[ctrl] {
		t.Fatal("combination released caller-held ctrl")
	}
	if err := b.KeyUp(ctx, "ctrl"); err != nil {
		t.Fatal(err)
	}
	if b.heldKeys[ctrl] {
		t.Fatal("ctrl keyup left held key behind")
	}
	if err := b.Type(ctx, "plain text {enter}"); err != nil {
		t.Fatalf("EIS text and key combination: %v", err)
	}
	if err := b.TypeLiteral(ctx, "literal{ctrl+x} café"); err != nil {
		t.Fatalf("literal Unicode text must not interpret syntax: %v", err)
	}
	if err := b.MouseMove(ctx, 44, 65); err != nil {
		t.Fatal(err)
	}
	if !b.hasPointer || b.pointerX != 44 || b.pointerY != 65 {
		t.Fatalf("pointer location state not recorded: %d,%d has=%v", b.pointerX, b.pointerY, b.hasPointer)
	}
	if err := b.MouseDown(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if !b.buttons[0x110] {
		t.Fatal("left mouse button not recorded as held")
	}
	if err := b.MouseUp(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if b.buttons[0x110] {
		t.Fatal("button release not recorded")
	}
	if err := b.MouseClick(ctx, 60, 70, 3); err != nil {
		t.Fatal(err)
	}
	if b.buttons[0x111] || b.pointerX != 60 || b.pointerY != 70 {
		t.Fatalf("click left held input or lost location %+v", b.buttons)
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"scroll up", func() error { return b.ScrollUp(ctx, 2) }}, {"scroll down", func() error { return b.ScrollDown(ctx, 3) }},
		{"scroll left", func() error { return b.ScrollLeft(ctx, 1) }}, {"scroll right", func() error { return b.ScrollRight(ctx, 5) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err != nil {
				t.Fatal(err)
			}
		})
	}
	space, err := b.PointerCoordinateSpace(ctx)
	if err != nil || space.Kind != CoordinateSpaceLogical || space.ScaleX != 1 || space.ScaleY != 1 {
		t.Fatalf("EIS coordinate space %+v %v", space, err)
	}
	if diagnostics := strings.Join(b.Diagnostics(), " "); !strings.Contains(diagnostics, "EIS") || !strings.Contains(diagnostics, "pointer") {
		t.Fatalf("authorized backend lacks diagnostics: %q", diagnostics)
	}
	if _, _, err := b.PointerLocation(ctx); err == nil {
		t.Fatal("EIS incorrectly fabricated absolute pointer position")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.KeyDown(ctx, "a"); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("closed portal accepted input: %v", err)
	}
}
func TestRemoteDesktopAuthorizedValidationDoesNotMutateEISState(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 80, height: 60, scale: 1}}}}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"unsupported key", func() error { return b.KeyDown(ctx, "no-such-key") }},
		{"invalid text syntax", func() error { return b.Type(ctx, "abc{unclosed") }},
		{"unknown named key at tail", func() error { return b.Type(ctx, "text{no-such-key}") }},
		{"outside allowed pointer region", func() error { return b.MouseMove(ctx, 100, 100) }},
		{"negative absolute pointer", func() error { return b.MouseMove(ctx, -1, 0) }},
		{"pointer left of region", func() error { return b.MouseClick(ctx, -1, 10, 1) }},
		{"invalid button", func() error { return b.MouseClick(ctx, 10, 10, 0) }},
		{"invalid button down", func() error { return b.MouseDown(ctx, 8) }},
		{"invalid button up", func() error { return b.MouseUp(ctx, -1) }},
		{"negative scroll", func() error { return b.ScrollDown(ctx, -1) }},
		{"oversized scroll", func() error { return b.ScrollUp(ctx, 1<<25) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("unauthorized or malformed EIS operation accepted")
			}
			if b.hasPointer || len(b.buttons) != 0 || len(b.heldKeys) != 0 {
				t.Fatalf("invalid operation mutated input state: pointer=%v buttons=%v keys=%v", b.hasPointer, b.buttons, b.heldKeys)
			}
			if stateErr := b.stateError(); stateErr != nil {
				t.Fatalf("local validation error revoked healthy session: %v", stateErr)
			}
		})
	}
	if err := b.ScrollDown(ctx, 0); err != nil {
		t.Fatal(err)
	}
}
