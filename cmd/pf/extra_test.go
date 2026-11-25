package main

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted"
	diagnostic "github.com/nskaggs/perfuncted/internal/diagnostic"
)

func TestDiagnosticEnvironmentFiltersAndRedacts(t *testing.T) {
	got := diagnostic.Environment([]string{
		"DISPLAY=:0",
		"WAYLAND_DISPLAY=wayland-1",
		"XDG_CURRENT_DESKTOP=Sway",
		"XDG_RUNTIME_DIR=/run/user/1000",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus;nonce=synthetic",
		"PF_AUDIT_SECRET=synthetic-secret",
	})

	if got["DISPLAY"] != ":0" || got["WAYLAND_DISPLAY"] != "wayland-1" {
		t.Fatalf("diagnostic environment lost safe display metadata: %v", got)
	}
	if got["XDG_CURRENT_DESKTOP"] != "Sway" {
		t.Fatalf("diagnostic environment lost desktop metadata: %v", got)
	}
	if got["XDG_RUNTIME_DIR"] != "<set>" || got["DBUS_SESSION_BUS_ADDRESS"] != "<set>" {
		t.Fatalf("diagnostic environment did not redact routing paths: %v", got)
	}
	if _, ok := got["PF_AUDIT_SECRET"]; ok {
		t.Fatalf("diagnostic environment exposed inherited secret: %v", got)
	}
}

func TestBuildInfoReportUsesSessionRuntime(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	t.Setenv("SWAYSOCK", "")

	session, err := perfuncted.Open(context.Background(), perfuncted.WithTarget(perfuncted.EnvironmentTarget([]string{
		"WAYLAND_DISPLAY=/tmp/perfuncted-session-wayland",
		"XDG_CURRENT_DESKTOP=Sway",
	})))
	if err != nil {
		t.Fatalf("perfuncted.Open: %v", err)
	}
	defer session.Close()

	got := buildInfoReport(session)
	if got, want := got.Compositor, "wlroots Wayland"; got != want {
		t.Fatalf("Compositor = %q, want %q", got, want)
	}
}
