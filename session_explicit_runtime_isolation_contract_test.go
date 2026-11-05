package perfuncted

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// An explicit target must not accidentally route desktop requests to the
// host's display, bus, or runtime directory when the target omits them.
func TestResolveRuntimeExplicitTargetNeverInheritsHostDesktop(t *testing.T) {
	t.Setenv("DISPLAY", ":987")
	t.Setenv("WAYLAND_DISPLAY", "host-wayland")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/host/private-bus")
	t.Setenv("XDG_RUNTIME_DIR", "/host/private-runtime")
	target := EnvironmentTarget([]string{"DISPLAY=:46", "DBUS_SESSION_BUS_ADDRESS=unix:path=/explicit/bus", "XDG_RUNTIME_DIR=/explicit/runtime", "TOKEN=private-target"})
	session := &Session{}
	if err := session.resolveRuntime(context.Background(), targetSelection{kind: TargetExplicit, target: target}, false); err != nil {
		t.Fatal(err)
	}
	if session.Target().Kind() != TargetExplicit || session.X11Display() != ":46" || session.XDG() != "/explicit/runtime" || session.DBusAddress() != "unix:path=/explicit/bus" || session.WaylandDisplay() != "" {
		t.Fatalf("explicit desktop routing contaminated: kind=%q x11=%q xdg=%q bus=%q wayland=%q", session.Target().Kind(), session.X11Display(), session.XDG(), session.DBusAddress(), session.WaylandDisplay())
	}
	copied := session.Env()
	for _, item := range copied {
		if strings.Contains(item, "/host/") || strings.Contains(item, "host-wayland") {
			t.Fatalf("host desktop leaked into explicit target: %q", item)
		}
	}
	copied[0] = "TOKEN=overwritten"
	if got := session.env.Get("TOKEN"); got != "private-target" {
		t.Fatalf("caller mutated session routing snapshot: %q", got)
	}
	targetCopy := session.Target().Env()
	if len(targetCopy) == 0 {
		t.Fatal("missing target environment")
	}
	targetCopy[0] = "TOKEN=overwritten"
	if got := session.env.Get("TOKEN"); got != "private-target" {
		t.Fatalf("caller mutated target routing snapshot: %q", got)
	}
}

func TestResolveRuntimeHostTargetUsesCurrentEnvironmentAndRejectsUnknownKind(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/synthetic-host-runtime")
	t.Setenv("DISPLAY", ":99")
	session := &Session{}
	if err := session.resolveRuntime(context.Background(), targetSelection{kind: TargetHost}, false); err != nil {
		t.Fatal(err)
	}
	if session.Target().Kind() != TargetHost || session.XDG() != "/synthetic-host-runtime" || session.X11Display() != ":99" {
		t.Fatalf("host routing mismatch: kind=%q xdg=%q display=%q", session.Target().Kind(), session.XDG(), session.X11Display())
	}
	original := session.Target()
	originalEnv := session.Env()
	err := session.resolveRuntime(context.Background(), targetSelection{kind: TargetKind("other-session")}, false)
	if err == nil || !strings.Contains(err.Error(), "unknown target kind") {
		t.Fatalf("unknown target was accepted: %v", err)
	}
	if session.Target().Kind() != original.Kind() || len(session.Env()) != len(originalEnv) {
		t.Fatalf("unknown target replaced current routing snapshot")
	}
	var nilSession *Session
	if len(nilSession.Env()) != 0 || nilSession.XDG() != "" || nilSession.DBusAddress() != "" || nilSession.WaylandDisplay() != "" || nilSession.X11Display() != "" {
		t.Fatal("nil session exposed environment data")
	}
	if env.FromEnviron(session.Env()).Get("XDG_RUNTIME_DIR") != "/synthetic-host-runtime" {
		t.Fatal("session Env lost host routing")
	}
}
