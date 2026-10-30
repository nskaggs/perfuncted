package input

import (
	"context"
	"errors"
	"testing"
)

// A portal session may fail after capability advertisement but before sender
// initialization. Every operation must fail with a typed error, never panic.
func TestRemoteDesktopIncompleteBackendFailsClosedWithoutPanic(t *testing.T) {
	for _, makeBackend := range []struct {
		name       string
		newBackend func() *RemoteDesktopBackend
	}{
		{"nil", func() *RemoteDesktopBackend { return nil }},
		{"zero-value", func() *RemoteDesktopBackend { return &RemoteDesktopBackend{} }},
	} {
		t.Run(makeBackend.name, func(t *testing.T) {
			b := makeBackend.newBackend()
			ops := []struct {
				name string
				run  func() error
			}{
				{"move", func() error { return b.MouseMove(context.Background(), 1, 2) }},
				{"click", func() error { return b.MouseClick(context.Background(), 1, 2, 1) }},
				{"down", func() error { return b.MouseDown(context.Background(), 1) }},
				{"up", func() error { return b.MouseUp(context.Background(), 1) }},
				{"key-down", func() error { return b.KeyDown(context.Background(), "a") }},
				{"key-up", func() error { return b.KeyUp(context.Background(), "a") }},
				{"literal", func() error { return b.TypeLiteral(context.Background(), "abc") }},
				{"type", func() error { return b.Type(context.Background(), "abc") }},
				{"scroll", func() error { return b.ScrollDown(context.Background(), 1) }},
				{"coordinate", func() error { _, err := b.PointerCoordinateSpace(context.Background()); return err }},
			}
			for _, op := range ops {
				t.Run(op.name, func(t *testing.T) {
					defer func() {
						if v := recover(); v != nil {
							t.Errorf("incomplete backend panicked: %v", v)
						}
					}()
					err := op.run()
					if !errors.Is(err, ErrRemoteDesktopUnavailable) {
						t.Errorf("error=%v, want unavailable", err)
					}
				})
			}
		})
	}
}

func TestRemoteDesktopInvalidButtonAndEmptyScrollHaveDefinedResults(t *testing.T) {
	b := &RemoteDesktopBackend{}
	for _, button := range []int{-1, 0, 4, 255} {
		if err := b.MouseClick(context.Background(), 0, 0, button); err == nil {
			t.Errorf("click invalid button %d accepted", button)
		}
		if err := b.MouseDown(context.Background(), button); err == nil {
			t.Errorf("down invalid button %d accepted", button)
		}
	}
	if err := b.ScrollDown(context.Background(), 0); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Errorf("scroll zero with unavailable backend=%v", err)
	}
}
