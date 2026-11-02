package screen

import (
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestScreenPortalProbeDistinguishesConnectedBusWithoutProviderFromAuthorizedProvider(t *testing.T) {
	address, _ := startSyntheticKWinBus(t, false)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	absent := checkPortalDbus(rt)
	if absent.Available || absent.Reason != "org.freedesktop.portal.Desktop not on session bus" {
		t.Fatalf("unregistered portal advertised: %+v", absent)
	}
	_, provider := newSyntheticScreenshotPortal(t)
	available := checkPortalDbus(env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + provider.busAddress}))
	if !available.Available || !strings.Contains(available.Reason, "on session bus") {
		t.Fatalf("healthy private portal not discovered: %+v", available)
	}
}

func TestScreenKWinProbeVerifiesRealPrivateServiceAuthorization(t *testing.T) {
	address, provider := startSyntheticKWinBus(t, true)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	available := checkKWinShot(rt, compositor.KDE)
	if !available.Available || available.Name != "kwin-shot2" {
		t.Fatalf("authorized private KWin was denied: %+v", available)
	}
	provider.mu.Lock()
	received := len(provider.captures)
	provider.mu.Unlock()
	if received == 0 {
		t.Fatalf("probe claimed screenshot without verifying capture")
	}
}
