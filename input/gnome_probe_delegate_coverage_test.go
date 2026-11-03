//go:build linux

package input

import (
	"strings"
	"testing"
)

// An unavailable GNOME bridge must never be advertised as authorized input.
func TestGnomeInputProbeFailsClosedWithoutBridge(t *testing.T) {
	rt := syntheticGNOMERuntime(t)
	result := checkGnomeNative(rt)
	if result.Available {
		t.Fatalf("missing GNOME input bridge advertised as available: %+v", result)
	}
	if !strings.Contains(strings.ToLower(result.Name), "gnome") {
		t.Fatalf("unexpected GNOME probe identity: %+v", result)
	}
	if result.Reason == "" {
		t.Fatalf("unavailable bridge produced no diagnostic: %+v", result)
	}
}
