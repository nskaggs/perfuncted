package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A configured pure-X11 session advertises X11 without attempting a network
// connection. Display probing remains usable even when the X server is down.
func TestOutputProbePureX11ReportsSelectedBackendWithoutConnecting(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=invalid-display-name", "WAYLAND_DISPLAY=", "XDG_RUNTIME_DIR="})
	results := ProbeRuntime(rt)
	if len(results) != 1 || results[0].Name != "x11" || !results[0].Available || !results[0].Selected || results[0].Reason != "DISPLAY set" {
		t.Fatalf("pure X11 backend advertisement = %+v, want selected available x11", results)
	}
}
