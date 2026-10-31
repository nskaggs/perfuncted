package diagnostic

import (
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
)

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
