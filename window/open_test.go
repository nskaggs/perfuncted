package window

import (
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntime_NoSessionReturnsError(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	if _, err := OpenRuntime(rt); err == nil {
		t.Fatal("OpenRuntime succeeded unexpectedly without a reachable backend")
	} else if !strings.Contains(err.Error(), "unsupported Wayland compositor") {
		t.Fatalf("OpenRuntime error = %v, want unsupported-session error", err)
	}
}

func TestOpenRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	mgr, err := OpenRuntime(rt)
	if err != nil {
		if !strings.Contains(err.Error(), `window/x11: connect to display ":99"`) {
			t.Fatalf("OpenRuntime error = %v, want X11 connection error", err)
		}
		return
	}
	if _, ok := mgr.(*X11Backend); !ok {
		t.Fatalf("OpenRuntime type = %T, want *X11Backend", mgr)
	}
}

func TestProbeRuntime_NoSessionReportsBackends(t *testing.T) {
	rt := env.FromEnviron([]string{})

	got := ProbeRuntime(rt)
	if len(got) != 3 {
		t.Fatalf("ProbeRuntime len = %d, want 3", len(got))
	}
	wantNames := []string{"kwin-scripting", "gnome-shell-eval", "foreign-toplevel"}
	for i, name := range wantNames {
		if got[i].Name != name {
			t.Fatalf("ProbeRuntime[%d].Name = %q, want %q", i, got[i].Name, name)
		}
		if got[i].Available || got[i].Selected {
			t.Fatalf("ProbeRuntime[%d] available=%v selected=%v, want false/false", i, got[i].Available, got[i].Selected)
		}
		if got[i].Reason == "" {
			t.Fatalf("ProbeRuntime[%d] missing reason", i)
		}
	}
}
