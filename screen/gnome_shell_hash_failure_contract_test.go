//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
)

// The hash and resolution adapters must never convert a failed GNOME capture
// into apparently valid black pixels, a zero checksum, or screen dimensions.
func TestGnomeShellHashAndResolutionPropagateRevokedAuthorization(t *testing.T) {
	backend, provider := newSyntheticGnomeScreenshotBackend(t)
	provider.mu.Lock()
	provider.deny = true
	provider.mu.Unlock()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		hash func() (uint32, error)
	}{
		{"full", func() (uint32, error) { return backend.GrabFullHash(ctx) }},
		{"region", func() (uint32, error) { return backend.GrabRegionHash(ctx, image.Rect(1, 1, 3, 3)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum, err := tc.hash()
			if sum != 0 || err == nil || !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("revoked capture returned hash=%x err=%v", sum, err)
			}
		})
	}
	w, h, err := backend.Resolution()
	if w != 0 || h != 0 || err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("revoked resolution returned %dx%d err=%v", w, h, err)
	}
}

func TestGnomeShellCaptureRejectsUnavailableAndCanceledBackend(t *testing.T) {
	var backend *GnomeShellScreenshotBackend
	for _, tc := range []struct {
		name string
		f    func() error
	}{
		{"nil grab", func() error { _, err := backend.Grab(context.Background(), image.Rectangle{}); return err }},
		{"nil full hash", func() error { _, err := backend.GrabFullHash(context.Background()); return err }},
		{"nil region hash", func() error {
			_, err := backend.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1))
			return err
		}},
		{"nil resolution", func() error { _, _, err := backend.Resolution(); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.f(); err == nil || !strings.Contains(err.Error(), "not initialized") {
				t.Fatalf("nil backend success: %v", err)
			}
		})
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("nil backend close: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewGnomeShellScreenshotBackendForBusContext(ctx, "invalid-bus"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor reached DBus: %v", err)
	}
}
