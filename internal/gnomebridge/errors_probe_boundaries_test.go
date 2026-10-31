//go:build linux

package gnomebridge

import (
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestGNOMEErrorTypesPreserveSentinelIdentity(t *testing.T) {
	var nilProtocol *ProtocolError
	if nilProtocol.Error() != "<nil>" {
		t.Fatalf("nil protocol error: %q", nilProtocol.Error())
	}
	protocol := &ProtocolError{Expected: 8, Actual: 3}
	if !errors.Is(protocol, ErrProtocolMismatch) || !strings.Contains(protocol.Error(), "3") || !strings.Contains(protocol.Error(), "8") {
		t.Fatalf("protocol mismatch: %v", protocol)
	}
	if errors.Is(protocol, ErrSessionRestartRequired) {
		t.Fatal("protocol error matched unrelated sentinel")
	}
	var nilRestart *SessionRestartRequiredError
	if nilRestart.Error() != "<nil>" {
		t.Fatalf("nil restart error: %q", nilRestart.Error())
	}
	for _, path := range []string{"", "/synthetic/gnome/extension"} {
		restart := &SessionRestartRequiredError{Path: path}
		if !errors.Is(restart, ErrSessionRestartRequired) || errors.Is(restart, ErrProtocolMismatch) {
			t.Fatalf("restart error identity: %v", restart)
		}
		if path != "" && !strings.Contains(restart.Error(), path) {
			t.Fatalf("restart missing path: %v", restart)
		}
	}
}

func TestGNOMEPublicCapabilityProbeFailsClosedOffline(t *testing.T) {
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=missing-pf", "XDG_CURRENT_DESKTOP=GNOME", "DBUS_SESSION_BUS_ADDRESS=unix:path=/missing-pf-session-bus"})
	for _, capability := range []string{CapabilityWindows, CapabilityScreen, CapabilityInput, CapabilityClipboard, "unrecognized"} {
		result := ProbeCapability(rt, capability)
		if result.Available || result.Name != "gnome-native" || result.Reason == "" {
			t.Fatalf("capability %s unexpected result: %+v", capability, result)
		}
	}
}
