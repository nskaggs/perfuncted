package screen

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// These isolated runtimes have no real Wayland socket or session D-Bus. The
// backend selector must preserve compositor-specific failures rather than
// silently falling back to unauthorized desktop capture.
func TestScreenBackendSelectionRequiresAdvertisedCompositorAuthority(t *testing.T) {
	for _, tc := range []struct{ name, desktop, match string }{
		{"wlroots", "sway", "no screencopy protocol"},
		{"KDE", "KDE", "requires KWin"},
		{"unknown", "UnrecognizedSyntheticDesktop", "unsupported Wayland"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := env.FromEnviron([]string{
				"XDG_CURRENT_DESKTOP=" + tc.desktop,
				"WAYLAND_DISPLAY=wayland-no-server",
				"XDG_RUNTIME_DIR=" + filepath.Join(t.TempDir(), "private-runtime"),
				"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-screen-bus",
			})
			backend, err := OpenRuntimeContext(context.Background(), rt)
			if backend != nil {
				_ = backend.Close()
				t.Fatalf("unauthorized backend selected: %T", backend)
			}
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("compositor %q failure was not specific: %v", tc.desktop, err)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			backend, err = OpenRuntimeContext(canceled, rt)
			if backend != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled screen open dispatched: %T, %v", backend, err)
			}
		})
	}
}

func TestScreenResolutionFallbackRejectsCanceledOrMalformedProviderResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := validatedResolution(ctx, 1920, 1080, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolver trusted dimensions: %v", err)
	}
	for _, dimensions := range [][2]int{{0, 1080}, {1920, 0}, {-1, 25}} {
		if _, _, err := validatedResolution(context.Background(), dimensions[0], dimensions[1], nil); err == nil {
			t.Fatalf("invalid resolution accepted: %v", dimensions)
		}
	}
	failure := errors.New("synthetic display resolution failure")
	if _, _, err := validatedResolution(context.Background(), 1920, 1080, failure); !errors.Is(err, failure) {
		t.Fatalf("resolver failure lost: %v", err)
	}
}
