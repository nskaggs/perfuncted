//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// GNOME's D-Bus adapter may acknowledge a command just as the caller cancels.
// A successful late acknowledgment is not a successful caller operation.
type cancelingGnomeBridge struct {
	recordingGnomeBridge
	cancel context.CancelFunc
}

func (b *cancelingGnomeBridge) Key(ctx context.Context, keyval uint32, pressed bool) error {
	err := b.recordingGnomeBridge.Key(ctx, keyval, pressed)
	if b.cancel != nil {
		b.cancel()
	}
	return err
}
func (b *cancelingGnomeBridge) Text(context.Context, string) error {
	if b.cancel != nil {
		b.cancel()
	}
	return nil
}
func (b *cancelingGnomeBridge) PointerLocation(context.Context) (int, int, error) {
	if b.cancel != nil {
		b.cancel()
	}
	return 72, 83, nil
}
func TestGnomeNativeLateProviderSuccessCannotOutrunCallerCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(context.Context, *GnomeNativeBackend) error
	}{
		{"key-down", func(ctx context.Context, b *GnomeNativeBackend) error { return b.KeyDown(ctx, "a") }},
		{"literal-text", func(ctx context.Context, b *GnomeNativeBackend) error { return b.TypeLiteral(ctx, "abc") }},
		{"pointer-location", func(ctx context.Context, b *GnomeNativeBackend) error {
			_, _, err := b.PointerLocation(ctx)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bridge := &cancelingGnomeBridge{cancel: cancel}
			b := &GnomeNativeBackend{bridge: bridge}
			if err := tc.run(ctx, b); !errors.Is(err, context.Canceled) {
				t.Fatalf("late provider success after cancellation accepted: %v", err)
			}
		})
	}
}
