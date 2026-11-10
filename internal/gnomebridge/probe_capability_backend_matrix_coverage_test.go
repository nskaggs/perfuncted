package gnomebridge

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Every backend must use the negotiated GNOME bridge capability rather than
// inferring access from desktop identity alone.
func TestProbeCapabilityCrossBackendAdvertisementMatrix(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/private-session",
	})
	for _, tc := range []struct{ capability, detail string }{
		{CapabilityWindows, "windows interface"},
		{CapabilityScreen, "screen interface"},
		{CapabilityInput, "virtual-input interface"},
		{CapabilityClipboard, "clipboard interface"},
		{"synthetic-capability", "synthetic-capability interface"},
	} {
		t.Run(tc.capability, func(t *testing.T) {
			got := probeCapability(rt, tc.capability, func(ctx context.Context, address string) (*Client, error) {
				if ctx == nil || address != rt.Get("DBUS_SESSION_BUS_ADDRESS") {
					t.Fatalf("wrong bus context or address: %q", address)
				}
				return &Client{caps: []string{tc.capability}}, nil
			})
			if !got.Available || got.Name != "gnome-native" || !strings.Contains(got.Reason, tc.detail) {
				t.Fatalf("advertised capability %q unavailable: %+v", tc.capability, got)
			}
		})
	}
}

func TestProbeCapabilityNonGNOMENeverConnectsToSessionBus(t *testing.T) {
	rt := env.FromEnviron([]string{"XDG_CURRENT_DESKTOP=KDE", "WAYLAND_DISPLAY=wayland-0", "DBUS_SESSION_BUS_ADDRESS=unix:path=/private-session"})
	result := probeCapability(rt, CapabilityInput, func(context.Context, string) (*Client, error) {
		t.Fatal("GNOME bridge probed outside GNOME session")
		return nil, nil
	})
	if result.Available || result.Name != "gnome-native" || result.Reason != "not a GNOME session" {
		t.Fatalf("non-GNOME session improperly admitted: %+v", result)
	}
}
