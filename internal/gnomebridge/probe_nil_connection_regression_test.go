package gnomebridge

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A failed connection must never be advertised as live authority or panic,
// including when a connector unexpectedly returns no error and no client.
func TestProbeCapabilityRejectsMissingClientWithoutPanic(t *testing.T) {
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-0", "XDG_CURRENT_DESKTOP=GNOME", "DBUS_SESSION_BUS_ADDRESS=unix:path=/private-session"})
	result := probeCapability(rt, CapabilityInput, func(context.Context, string) (*Client, error) { return nil, nil })
	if result.Available || result.Name != "gnome-native" || !strings.Contains(result.Reason, "does not advertise") {
		t.Fatalf("missing client was not rejected: %+v", result)
	}
}
