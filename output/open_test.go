package output

import (
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntime_NoSessionReturnsError(t *testing.T) {
	rt := env.FromEnviron([]string{})

	if _, err := OpenRuntime(rt); err == nil {
		t.Fatal("OpenRuntime succeeded unexpectedly without DISPLAY or Wayland socket")
	} else if !strings.Contains(err.Error(), "no display or Wayland socket available") {
		t.Fatalf("OpenRuntime error = %v, want no-session error", err)
	}
}

func TestOpenRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	lst, err := OpenRuntime(rt)
	if err != nil {
		if !strings.Contains(err.Error(), `output/x11: connect to display ":99"`) {
			t.Fatalf("OpenRuntime error = %v, want X11 connection error", err)
		}
		return
	}
	if _, ok := lst.(*X11Lister); !ok {
		t.Fatalf("OpenRuntime type = %T, want *X11Lister", lst)
	}
}

func TestProbeRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	got := ProbeRuntime(rt)
	if len(got) != 1 {
		t.Fatalf("ProbeRuntime len = %d, want 1", len(got))
	}
	if got[0].Name != "x11" {
		t.Fatalf("ProbeRuntime name = %q, want x11", got[0].Name)
	}
	if !got[0].Selected || !got[0].Available {
		t.Fatalf("ProbeRuntime selected=%v available=%v, want true/true", got[0].Selected, got[0].Available)
	}
	if !strings.Contains(got[0].Reason, "socket missing") {
		t.Fatalf("ProbeRuntime reason = %q, want missing socket fallback", got[0].Reason)
	}
}

func TestProbeRuntime_NoSessionReportsUnavailable(t *testing.T) {
	rt := env.FromEnviron([]string{})

	got := ProbeRuntime(rt)
	if len(got) != 1 {
		t.Fatalf("ProbeRuntime len = %d, want 1", len(got))
	}
	if got[0].Name != "output" {
		t.Fatalf("ProbeRuntime name = %q, want output", got[0].Name)
	}
	if got[0].Available || got[0].Selected {
		t.Fatalf("ProbeRuntime available=%v selected=%v, want false/false", got[0].Available, got[0].Selected)
	}
	if !strings.Contains(got[0].Reason, "no output source available") {
		t.Fatalf("ProbeRuntime reason = %q, want no-output-source message", got[0].Reason)
	}
}
