//go:build linux
// +build linux

package gnomebridge

import (
	"context"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
)

// ProbeCapability returns a probe result for the GNOME native bridge
// advertising capability. It centralizes session-kind gating, D-Bus
// negotiation, and capability checking used by window, input, screen, and
// clipboard backends.
func ProbeCapability(rt env.Runtime, capability string) probe.Result {
	r := probe.Result{Name: "gnome-native"}
	if compositor.DetectRuntime(rt) != compositor.GNOME {
		r.Reason = "not a GNOME session"
		return r
	}
	bridge, err := NewClientForBus(context.Background(), rt.Get("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		r.Reason = err.Error()
		return r
	}
	defer bridge.Close()
	if !bridge.HasCapability(capability) {
		r.Reason = "bridge does not advertise " + capability + " capability"
		return r
	}
	r.Available = true
	switch capability {
	case CapabilityWindows:
		r.Reason = "bundled GNOME bridge windows interface"
	case CapabilityScreen:
		r.Reason = "bundled GNOME bridge screen interface"
	case CapabilityInput:
		r.Reason = "bundled GNOME bridge virtual-input interface"
	case CapabilityClipboard:
		r.Reason = "bundled GNOME bridge clipboard interface"
	default:
		r.Reason = "bundled GNOME bridge " + capability + " interface"
	}
	return r
}
