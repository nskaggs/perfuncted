package perfuncted

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestResolveSessionEnvPrefersExplicitValues(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")

	env, err := resolveSessionEnv(Options{
		XDGRuntimeDir:      "/tmp/perfuncted-xdg-test",
		WaylandDisplay:     "wayland-9",
		DBusSessionAddress: "unix:path=/tmp/perfuncted-xdg-test/bus",
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.xdgRuntimeDir != "/tmp/perfuncted-xdg-test" {
		t.Fatalf("xdg = %q", env.xdgRuntimeDir)
	}
	if env.waylandDisplay != "wayland-9" {
		t.Fatalf("wayland = %q", env.waylandDisplay)
	}
	if env.dbusSessionAddress != "unix:path=/tmp/perfuncted-xdg-test/bus" {
		t.Fatalf("dbus = %q", env.dbusSessionAddress)
	}
}

func TestApplySessionEnvRestoresPreviousProcessEnv(t *testing.T) {
	// initial process env
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")

	// apply overrides via package-level helpers
	restore := applySessionEnv(sessionEnv{
		xdgRuntimeDir:      "/tmp/perfuncted-xdg-test",
		waylandDisplay:     "wayland-9",
		dbusSessionAddress: "unix:path=/tmp/perfuncted-xdg-test/bus",
	})

	// wl.SocketPath should reflect the override (xdg + wayland name)
	if got := wl.SocketPath(); got != "/tmp/perfuncted-xdg-test/wayland-9" {
		t.Fatalf("SocketPath = %q", got)
	}

	restore()

	// after restore, sockets are back to using the process env
	if got := wl.SocketPath(); got != "/run/user/1000/wayland-0" {
		t.Fatalf("restored SocketPath = %q", got)
	}
}
