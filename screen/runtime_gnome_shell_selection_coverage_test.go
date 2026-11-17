package screen

import (
	"context"
	"image"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestScreenRuntimeSelectsAuthorizedGnomeShellCaptureBeforePortal(t *testing.T) {
	_, portal := newSyntheticScreenshotPortal(t)
	shell := &syntheticGnomeScreenshot{success: true}
	reply, err := portal.conn.RequestName(gnomeShellShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim synthetic GNOME Shell screenshot service: reply=%v err=%v", reply, err)
	}
	if err := portal.conn.Export(shell, dbus.ObjectPath(gnomeShellShotPath), gnomeShellShotIface); err != nil {
		t.Fatalf("export synthetic GNOME Shell screenshot service: %v", err)
	}

	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-without-synthetic-bridge",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	backend, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("runtime failed to select the available GNOME Shell backend: %v", err)
	}
	defer backend.Close()
	if _, ok := backend.(*GnomeShellScreenshotBackend); !ok {
		t.Fatalf("runtime selected %T, want the authorized GNOME Shell backend", backend)
	}
	img, err := backend.Grab(ctx, image.Rectangle{})
	if err != nil {
		t.Fatalf("synthetic GNOME Shell capture: %v", err)
	}
	if img == nil {
		t.Fatal("synthetic GNOME Shell capture returned nil image")
	}
	if img.Bounds().Size() != image.Pt(4, 4) {
		t.Fatalf("synthetic GNOME Shell capture bounds=%v", img.Bounds())
	}
	shell.mu.Lock()
	captures := append([]string(nil), shell.observed...)
	shell.mu.Unlock()
	if len(captures) < 2 || captures[0] != "area" || captures[len(captures)-1] != "full" {
		t.Fatalf("runtime authorization and capture sequence = %v, want area probe then full capture", captures)
	}
}
