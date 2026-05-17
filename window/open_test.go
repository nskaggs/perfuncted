package window

import (
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
	})

	_, err := OpenRuntime(rt)
	if err == nil {
		t.Fatal("OpenRuntime succeeded unexpectedly")
	}
	if !strings.Contains(err.Error(), `window/x11: connect to display ":99"`) {
		t.Fatalf("OpenRuntime error = %v, want X11 connection error", err)
	}
}
