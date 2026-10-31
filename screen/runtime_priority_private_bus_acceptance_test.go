package screen

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Real private D-Bus providers exercise the runtime fallback ordering without
// browsing, authorizing or capturing the actual desktop.
func TestScreenRuntimeSelectsKWinScreenshotOverFallback(t *testing.T) {
	address, provider := startSyntheticKWinBus(t, true)
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/nonexistent/perfuncted-screen-wire",
		"XDG_CURRENT_DESKTOP=KDE",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	backend, err := OpenRuntimeContext(ctx, rt)
	if err != nil {
		t.Fatalf("authoritative KDE screenshot service not selected: %v", err)
	}
	defer backend.Close()
	if _, ok := backend.(*KWinShotBackend); !ok {
		t.Fatalf("KDE selected %T rather than KWin FD screenshot protocol", backend)
	}
	img, err := backend.Grab(ctx, image.Rect(0, 0, 2, 2))
	if err != nil || img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("runtime KWin screenshot failed: %v image=%v", err, img)
	}
	provider.mu.Lock()
	count := len(provider.captures)
	provider.mu.Unlock()
	if count != 2 {
		t.Fatalf("KWin initialization/capture count=%d, want two", count)
	}
}
func TestScreenRuntimeFallbackToAuthorizedSyntheticPortalAcrossCompositors(t *testing.T) {
	for _, desktop := range []string{"KDE", "GNOME", "other"} {
		t.Run(desktop, func(t *testing.T) {
			_, portal := newSyntheticScreenshotPortal(t)
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/nonexistent/perfuncted-screen-wire",
				"XDG_CURRENT_DESKTOP=" + desktop,
				"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			backend, err := OpenRuntimeContext(ctx, rt)
			if err != nil {
				t.Fatalf("%s runtime did not reach available portal: %v", desktop, err)
			}
			defer backend.Close()
			if _, ok := backend.(*PortalDBusBackend); !ok {
				t.Fatalf("%s runtime selected %T instead of private portal provider", desktop, backend)
			}
			// The portal exists, but no screenshot has been authorized or supplied.
			// Successful backend selection must not manufacture captured pixels.
			image, err := backend.Grab(ctx, image.Rect(0, 0, 1, 1))
			if err == nil || image != nil {
				t.Fatalf("%s portal fabricated screenshot from empty URI: %v %v", desktop, image, err)
			}
		})
	}
}
func TestScreenRuntimeAbsentPrivateBusFailsClosedAcrossGNOMEAndKDE(t *testing.T) {
	for _, desktop := range []string{"GNOME", "KDE"} {
		t.Run(desktop, func(t *testing.T) {
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=missing-perfuncted-wayland",
				"XDG_RUNTIME_DIR=" + t.TempDir(),
				"XDG_CURRENT_DESKTOP=" + desktop,
				"DBUS_SESSION_BUS_ADDRESS=unix:path=" + t.TempDir() + "/absent-bus",
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			backend, err := OpenRuntimeContext(ctx, rt)
			if backend != nil {
				defer backend.Close()
				t.Fatalf("%s opened unsupported desktop without an authenticated provider", desktop)
			}
			if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), desktop) {
				t.Fatalf("%s failed to report unavailable backend deterministically: %v", desktop, err)
			}
		})
	}
}
