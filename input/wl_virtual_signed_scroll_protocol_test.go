package input

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Virtual scroll requests must retain direction and axis through signed 24.8
// Wayland fixed-point encoding, not merely report successful API calls.
func TestWlVirtualScrollDirectionsEmitSignedAxisAndFrame(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scroll func(*WlVirtualBackend) error
		axis   uint32
		value  int32
	}{
		{"up", func(b *WlVirtualBackend) error { return b.ScrollUp(context.Background(), 2) }, 0, -2 * 15 * 256},
		{"down", func(b *WlVirtualBackend) error { return b.ScrollDown(context.Background(), 3) }, 0, 3 * 15 * 256},
		{"left", func(b *WlVirtualBackend) error { return b.ScrollLeft(context.Background(), 4) }, 1, -4 * 15 * 256},
		{"right", func(b *WlVirtualBackend) error { return b.ScrollRight(context.Background(), 5) }, 1, 5 * 15 * 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, peer := newCapturedWlVirtualBackend(t)
			if err := tc.scroll(backend); err != nil {
				t.Fatalf("scroll failed: %v", err)
			}
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			messages := readCapturedWlWrites(t, peer)
			if len(messages) != 2 {
				t.Fatalf("scroll emitted %d requests, want axis and frame", len(messages))
			}
			axis, frame := messages[0], messages[1]
			if len(axis) != 20 || wl.Uint32(axis[0:4]) != 7 || wl.Uint32(axis[4:8]) != 20<<16|3 || wl.Uint32(axis[12:16]) != tc.axis || int32(wl.Uint32(axis[16:20])) != tc.value {
				t.Fatalf("incorrect scroll axis wire frame: %v; want axis %d value %d", axis, tc.axis, tc.value)
			}
			if len(frame) != 8 || wl.Uint32(frame[0:4]) != 7 || wl.Uint32(frame[4:8]) != 8<<16|4 {
				t.Fatalf("missing virtual-pointer frame after scroll: %v", frame)
			}
		})
	}
}

func TestWlVirtualScrollInvalidMagnitudeNeverEmitsProtocol(t *testing.T) {
	backend, peer := newCapturedWlVirtualBackend(t)
	for _, invoke := range []func() error{
		func() error { return backend.ScrollUp(context.Background(), -1) },
		func() error { return backend.ScrollLeft(context.Background(), maxScrollClicks+1) },
		func() error { return backend.ScrollRight(context.Background(), -1) },
		func() error { return backend.ScrollDown(context.Background(), maxScrollClicks+1) },
	} {
		if err := invoke(); err == nil {
			t.Fatal("invalid scroll unexpectedly succeeded")
		}
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if got := readCapturedWlWrites(t, peer); len(got) != 0 {
		t.Fatalf("invalid scroll emitted %d protocol frames", len(got))
	}
}
