//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// Probe missing devices and canceled operations at the actual public method
// boundary without requiring /dev/uinput or a graphical session.
func TestUinputCanceledAndIncompleteBackendNeverInjectsEvents(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var b *UinputBackend
		if err := b.KeyDown(context.Background(), "ctrl"); err == nil {
			t.Fatal("nil backend injected keyboard event")
		}
		if _, err := b.PointerCoordinateSpace(context.Background()); !errors.Is(err, ErrNotSupported) {
			t.Fatalf("nil backend fabricated logical mapping: %v", err)
		}
	})
	t.Run("canceled authorized coordinate query", func(t *testing.T) {
		b, _ := newTestBackend(t)
		b.verifiedSingleLogicalOutput = true
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := b.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled pointer query returned %v", err)
		}
		if space, err := b.PointerCoordinateSpace(context.Background()); err != nil || space.Kind != CoordinateSpaceLogical {
			t.Fatalf("authorized logical coordinate space=%+v err=%v", space, err)
		}
	})
	t.Run("missing keyboard device", func(t *testing.T) {
		b := &UinputBackend{charToRune: qwertyRuneMap()}
		for _, tc := range []struct {
			name string
			call func() error
		}{
			{"key down", func() error { return b.KeyDown(context.Background(), "ctrl") }},
			{"key up", func() error { return b.KeyUp(context.Background(), "ctrl") }},
			{"literal", func() error { return b.TypeLiteral(context.Background(), "private") }},
			{"parsed", func() error { return b.Type(context.Background(), "private{enter}") }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if err := tc.call(); !errors.Is(err, ErrNotSupported) {
					t.Fatalf("missing uinput keyboard accepted command: %v", err)
				}
			})
		}
	})
	t.Run("cancel all input actions", func(t *testing.T) {
		b, kb := newTestBackend(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, tc := range []struct {
			name string
			call func() error
		}{
			{"key down", func() error { return b.KeyDown(ctx, "ctrl") }},
			{"key up", func() error { return b.KeyUp(ctx, "ctrl") }},
			{"type", func() error { return b.Type(ctx, "private{enter}") }},
			{"literal", func() error { return b.TypeLiteral(ctx, "private") }},
			{"mouse down", func() error { return b.MouseDown(ctx, 1) }},
			{"mouse up", func() error { return b.MouseUp(ctx, 1) }},
			{"scroll right", func() error { return b.ScrollRight(ctx, 1) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if err := tc.call(); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled input action returned %v", err)
				}
			})
		}
		if len(kb.events) != 0 {
			t.Fatalf("canceled input emitted keyboard events: %v", kb.events)
		}
	})
}
