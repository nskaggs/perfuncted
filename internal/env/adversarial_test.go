package env

import (
	"testing"
)

func TestAdversarialManagedSessionEnvIsFrozen(t *testing.T) {
	host := FromEnviron([]string{
		"AT_SPI_BUS=unix:abstract=/tmp/fake",
		"ATSPI_BUS_ADDRESS=unix:path=/tmp/a11y",
		"AT_SPI_BUS_ADDRESS=unix:path=/tmp/spi",
		"GTK_MODULES=gail:atk-bridge",
		"GTK_A11Y=1",
		"GNOME_ACCESSIBILITY=1",
		"QT_ACCESSIBILITY=1",
		"QT_LINUX_ACCESSIBILITY_ALWAYS_ON=1",
		"SWAYSOCK=/run/user/1000/sway.sock",
		"WAYLAND_DISPLAY=wayland-0",
		"DISPLAY=:0",
		"KEEP=1",
	})
	managed := host.WithSession("/tmp/xdg", "wayland-1", "unix:path=/tmp/dbus")
	for _, leaked := range []string{"AT_SPI_BUS", "ATSPI_BUS_ADDRESS", "AT_SPI_BUS_ADDRESS", "GTK_MODULES", "GTK_A11Y", "GNOME_ACCESSIBILITY", "QT_ACCESSIBILITY", "QT_LINUX_ACCESSIBILITY_ALWAYS_ON", "SWAYSOCK"} {
		if managed.Has(leaked) {
			t.Fatalf("managed session leaks %q", leaked)
		}
	}
	if got := managed.Get("XDG_SESSION_TYPE"); got != "wayland" {
		t.Fatalf("XDG_SESSION_TYPE = %q, want wayland", got)
	}
	if got := managed.Get("KEEP"); got != "1" {
		t.Fatalf("unrelated env was dropped: KEEP=%q", got)
	}
	withBus := managed.WithAccessibilityBus("unix:path=/tmp/atspi")
	if got := withBus.Get("ATSPI_BUS_ADDRESS"); got != "unix:path=/tmp/atspi" {
		t.Fatalf("ATSPI_BUS_ADDRESS = %q", got)
	}
	if withBus.Has("AT_SPI_BUS") || withBus.Has("AT_SPI_BUS_ADDRESS") {
		t.Fatal("accessibility bus snapshot retains X-root variables")
	}
	cleared := managed.WithAccessibilityBus("")
	if cleared.Has("ATSPI_BUS_ADDRESS") {
		t.Fatal("empty accessibility address did not clear ATSPI_BUS_ADDRESS")
	}
}
