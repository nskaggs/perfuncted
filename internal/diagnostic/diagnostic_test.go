package diagnostic

import (
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
	"reflect"
	"strings"
	"testing"
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

func TestProbesOfflineEnvironmentRedactsRoutingAndEnumeratesSurfaces(t *testing.T) {
	const address = "unix:path=/unreachable-pf-dbus-bus-privacy-probe"
	const runtimeDir = "/unreachable-pf-runtime-privacy-probe"
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=missing-pf-wayland", "XDG_RUNTIME_DIR=" + runtimeDir, "DBUS_SESSION_BUS_ADDRESS=" + address, "XDG_CURRENT_DESKTOP=unknown"})
	results := Probes(rt)
	for _, name := range []string{"screen", "input", "window", "output"} {
		list, ok := results[name]
		if !ok || len(list) == 0 {
			t.Fatalf("missing %s results: %v", name, results)
		}
		for _, result := range list {
			if strings.Contains(result.Reason, address) || strings.Contains(result.Reason, runtimeDir) {
				t.Fatalf("%s leaked routing value: %+v", name, result)
			}
		}
	}
	if len(results) != 4 {
		t.Fatalf("unexpected diagnostic surface: %v", results)
	}
}

func TestRedactProbeResultsEmptyAndNoRoutingValues(t *testing.T) {
	if out := RedactProbeResults(nil, []string{"XDG_RUNTIME_DIR=/secret"}); out != nil {
		t.Fatalf("nil input = %v", out)
	}
	original := []probe.Result{{Name: "ok", Reason: "unrelated failure"}}
	out := RedactProbeResults(original, []string{"XDG_RUNTIME_DIR=", "DBUS_SESSION_BUS_ADDRESS="})
	if len(out) != 1 || out[0].Reason != "unrelated failure" {
		t.Fatalf("unexpected redaction: %v", out)
	}
	out[0].Reason = "changed"
	if original[0].Reason != "unrelated failure" {
		t.Fatal("caller result aliased source")
	}
}
