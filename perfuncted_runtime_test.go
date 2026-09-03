package perfuncted

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestResolveRuntimePreservesHostDesktopWhenNoSessionOverride(t *testing.T) {
	t.Setenv("DISPLAY", ":99")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")

	session, err := Open(context.Background())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})
	if got := session.env.Display(); got != ":99" {
		t.Fatalf("display = %q, want :99", got)
	}
}

func TestExplicitTargetUsesExactImmutableEnvironment(t *testing.T) {
	environment := []string{
		"XDG_RUNTIME_DIR=/tmp/perfuncted-xdg-test",
		"WAYLAND_DISPLAY=wayland-1",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/perfuncted-xdg-test/bus",
		"DISPLAY=",
		"SWAYSOCK=",
	}
	session, err := Open(
		context.Background(),
		WithTarget(EnvironmentTarget(environment)),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})
	environment[0] = "XDG_RUNTIME_DIR=/changed"
	targetEnv := env.FromEnviron(session.Target().Env())
	if got := targetEnv.Get("XDG_RUNTIME_DIR"); got != "/tmp/perfuncted-xdg-test" {
		t.Fatalf("XDG_RUNTIME_DIR = %q", got)
	}
	if got := targetEnv.Get("WAYLAND_DISPLAY"); got != "wayland-1" {
		t.Fatalf("WAYLAND_DISPLAY = %q", got)
	}
	if got := targetEnv.Display(); got != "" {
		t.Fatalf("DISPLAY = %q, want empty", got)
	}
	if got := targetEnv.Get("SWAYSOCK"); got != "" {
		t.Fatalf("SWAYSOCK = %q, want empty", got)
	}
}

func TestLaunchWithExplicitTargetDoesNotInheritHostEnvironment(t *testing.T) {
	t.Setenv("PERFUNCTED_HOST_ONLY", "host-value")
	t.Setenv("DISPLAY", ":host")

	session, err := Open(
		context.Background(),
		WithTarget(EnvironmentTarget([]string{
			"WAYLAND_DISPLAY=target-wayland",
		})),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	var stdout strings.Builder
	app, err := session.Launch(
		context.Background(),
		Command{
			Name: "sh",
			Args: []string{
				"-c",
				`printf '%s|%s|%s' "$PERFUNCTED_HOST_ONLY" "$DISPLAY" "$WAYLAND_DISPLAY"`,
			},
			Stdout: &stdout,
		},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := stdout.String(); got != "||target-wayland" {
		t.Fatalf("stdout = %q, want ||target-wayland", got)
	}
}

func TestRuntimeSocketPathResolvesRelativeWaylandDisplay(t *testing.T) {
	rt := env.FromEnviron([]string{
		"XDG_RUNTIME_DIR=/tmp/perfuncted-host",
		"WAYLAND_DISPLAY=wayland-0",
	})

	if got := rt.SocketPath(); got != "/tmp/perfuncted-host/wayland-0" {
		t.Fatalf("SocketPath = %q, want /tmp/perfuncted-host/wayland-0", got)
	}
}

func TestRuntimeSocketPathKeepsAbsoluteWaylandDisplay(t *testing.T) {
	rt := env.FromEnviron([]string{
		"XDG_RUNTIME_DIR=/tmp/perfuncted-host",
		"WAYLAND_DISPLAY=/run/user/1000/wayland-1",
	})

	if got := rt.SocketPath(); got != "/run/user/1000/wayland-1" {
		t.Fatalf("SocketPath = %q, want /run/user/1000/wayland-1", got)
	}
}

func TestRuntimeSocketPathReturnsEmptyWithoutRuntimeDir(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-0",
	})

	if got := rt.SocketPath(); got != "" {
		t.Fatalf("SocketPath = %q, want empty", got)
	}
}
