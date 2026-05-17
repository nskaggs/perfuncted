package screen

import (
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	scr, err := OpenRuntime(rt)
	if err != nil {
		if !strings.Contains(err.Error(), `screen/x11: connect to display ":99"`) {
			t.Fatalf("OpenRuntime error = %v, want X11 connection error", err)
		}
		return
	}
	if _, ok := scr.(*X11Backend); !ok {
		t.Fatalf("OpenRuntime type = %T, want *X11Backend", scr)
	}
}
