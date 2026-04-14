package session

import (
	"image"
	"os"
	"strings"
	"testing"
)

func TestEnviron(t *testing.T) {
	env := Environ("/tmp/test-xdg", "wayland-99", "unix:path=/tmp/test-xdg/bus")

	var xdg, wl, dbus, display, gdk, qt string
	for _, e := range env {
		switch {
		case strings.HasPrefix(e, "XDG_RUNTIME_DIR="):
			xdg = strings.TrimPrefix(e, "XDG_RUNTIME_DIR=")
		case strings.HasPrefix(e, "WAYLAND_DISPLAY="):
			wl = strings.TrimPrefix(e, "WAYLAND_DISPLAY=")
		case strings.HasPrefix(e, "DBUS_SESSION_BUS_ADDRESS="):
			dbus = strings.TrimPrefix(e, "DBUS_SESSION_BUS_ADDRESS=")
		case strings.HasPrefix(e, "DISPLAY="):
			display = strings.TrimPrefix(e, "DISPLAY=")
		case strings.HasPrefix(e, "GDK_BACKEND="):
			gdk = strings.TrimPrefix(e, "GDK_BACKEND=")
		case strings.HasPrefix(e, "QT_QPA_PLATFORM="):
			qt = strings.TrimPrefix(e, "QT_QPA_PLATFORM=")
		}
	}

	if xdg != "/tmp/test-xdg" {
		t.Errorf("XDG_RUNTIME_DIR = %q, want /tmp/test-xdg", xdg)
	}
	if wl != "wayland-99" {
		t.Errorf("WAYLAND_DISPLAY = %q, want wayland-99", wl)
	}
	if dbus != "unix:path=/tmp/test-xdg/bus" {
		t.Errorf("DBUS_SESSION_BUS_ADDRESS = %q", dbus)
	}
	if display != "" {
		t.Errorf("DISPLAY = %q, want empty (cleared)", display)
	}
	if gdk != "wayland" {
		t.Errorf("GDK_BACKEND = %q, want wayland", gdk)
	}
	if qt != "wayland" {
		t.Errorf("QT_QPA_PLATFORM = %q, want wayland", qt)
	}
}

func TestEnvironFiltersHost(t *testing.T) {
	// Set some vars that should be filtered.
	os.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	os.Setenv("WAYLAND_DISPLAY", "wayland-0")
	os.Setenv("DISPLAY", ":0")
	defer os.Unsetenv("DISPLAY")

	env := Environ("/tmp/sess", "wayland-1", "unix:path=/tmp/sess/bus")

	// Count occurrences of XDG_RUNTIME_DIR — should be exactly 1.
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("XDG_RUNTIME_DIR appears %d times, want 1", count)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	if cfg.Resolution != (image.Point{}) {
		t.Errorf("default Resolution = %v, want zero", cfg.Resolution)
	}
	if cfg.LogDir != "" {
		t.Errorf("default LogDir = %q, want empty", cfg.LogDir)
	}
}

func TestEmbeddedConfigs(t *testing.T) {
	data, err := embeddedConfigs.ReadFile("configs/ci.conf")
	if err != nil {
		t.Fatalf("read embedded ci.conf: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded ci.conf is empty")
	}
	if !strings.Contains(string(data), "HEADLESS-1") {
		t.Error("ci.conf missing HEADLESS-1 output line")
	}

	data, err = embeddedConfigs.ReadFile("configs/headless.conf")
	if err != nil {
		t.Fatalf("read embedded headless.conf: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded headless.conf is empty")
	}
}
