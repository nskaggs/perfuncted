package diagnostic

import (
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/internal/probe"
)

func TestEnvironmentFiltersAndRedacts(t *testing.T) {
	t.Parallel()

	got := Environment([]string{
		"DISPLAY=:0",
		"WAYLAND_DISPLAY=wayland-1",
		"XDG_CURRENT_DESKTOP=Sway",
		"XDG_SESSION_TYPE=wayland",
		"XDG_RUNTIME_DIR=/run/user/1000",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus;nonce=synthetic",
		"PF_AUDIT_SECRET=synthetic-secret",
	})

	want := map[string]string{
		"DISPLAY":                  ":0",
		"WAYLAND_DISPLAY":          "wayland-1",
		"XDG_CURRENT_DESKTOP":      "Sway",
		"XDG_SESSION_TYPE":         "wayland",
		"XDG_RUNTIME_DIR":          "<set>",
		"DBUS_SESSION_BUS_ADDRESS": "<set>",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Environment() = %v, want %v", got, want)
	}
}

func TestRedactProbeResultsCopiesAndRedacts(t *testing.T) {
	t.Parallel()

	results := []probe.Result{{
		Name:   "synthetic",
		Reason: "connect unix:path=/run/user/1000/bus;nonce=secret in /run/user/1000",
	}}
	got := RedactProbeResults(results, []string{
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus;nonce=secret",
		"XDG_RUNTIME_DIR=/run/user/1000",
	})

	if got[0].Reason != "connect <set> in <set>" {
		t.Fatalf("redacted reason = %q, want routing values replaced", got[0].Reason)
	}
	if results[0].Reason == got[0].Reason {
		t.Fatal("RedactProbeResults mutated its input")
	}
}
