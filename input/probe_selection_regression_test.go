package input

import (
	"github.com/nskaggs/perfuncted/internal/env"
	"testing"
)

func TestProbeOrderMatchesWaylandBackendPriority(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=:not-a-display", "WAYLAND_DISPLAY=wayland-unknown", "XDG_RUNTIME_DIR=" + t.TempDir()})
	results := ProbeRuntime(rt)
	if len(results) < 3 {
		t.Fatalf("expected Wayland + uinput + XTest candidates, got %+v", results)
	}
	for i, want := range []string{"wl-virtual", "uinput", "xtest"} {
		if got := results[i].Name; got != want {
			t.Fatalf("probe %d=%s, want %s; selection order must match runtime", i, got, want)
		}
	}
}
func TestProbeOrderPureX11KeepsXTestFirst(t *testing.T) {
	results := ProbeRuntime(env.FromEnviron([]string{"DISPLAY=:not-a-display"}))
	if len(results) < 2 || results[0].Name != "xtest" || results[1].Name != "uinput" {
		t.Fatalf("pure X11 probes=%+v", results)
	}
}
