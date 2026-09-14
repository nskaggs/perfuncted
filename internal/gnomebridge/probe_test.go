//go:build linux
// +build linux

package gnomebridge

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
)

func TestProbeCapabilityPrefersAdvertisedGNOMEBridge(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/session",
	})
	got := probeCapability(rt, CapabilityInput, func(_ context.Context, address string) (*Client, error) {
		if address != rt.Get("DBUS_SESSION_BUS_ADDRESS") {
			t.Fatalf("bridge address = %q, want runtime session bus", address)
		}
		return &Client{caps: []string{CapabilityInput}}, nil
	})
	selected := probe.SelectBest([]probe.Result{
		got,
		{Name: "wl-virtual", Available: true, Reason: "fallback input backend"},
	})
	if !selected[0].Available || !selected[0].Selected || selected[1].Selected {
		t.Fatalf("GNOME capability selection = %+v, want advertised gnome-native selected before fallback", selected)
	}
}

func TestProbeCapabilityKeepsUnsupportedAndProtocolMismatchUnavailable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/session",
	})
	tests := []struct {
		name  string
		open  func(context.Context, string) (*Client, error)
		cause string
	}{
		{
			name: "unsupported capability",
			open: func(context.Context, string) (*Client, error) {
				return &Client{caps: []string{CapabilityWindows}}, nil
			},
			cause: "does not advertise input capability",
		},
		{
			name: "protocol mismatch",
			open: func(context.Context, string) (*Client, error) {
				return nil, &ProtocolError{Expected: ProtocolVersion, Actual: ProtocolVersion + 1}
			},
			cause: "incompatible",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := probeCapability(rt, CapabilityInput, test.open)
			if got.Available || got.Selected || !strings.Contains(got.Reason, test.cause) {
				t.Fatalf("probe result = %+v, want unavailable explicit %q reason", got, test.cause)
			}
			selected := probe.SelectBest([]probe.Result{
				got,
				{Name: "fallback", Available: true, Reason: "fallback backend"},
			})
			if selected[0].Selected || !selected[1].Selected {
				t.Fatalf("selection after %s = %+v, want explicit unavailable GNOME candidate and selected fallback", test.name, selected)
			}
		})
	}
}
