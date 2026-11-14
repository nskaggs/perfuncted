//go:build linux

package window

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
)

// The probe must verify a live, usable Shell.Eval capability on an isolated
// service, not infer availability merely from a GNOME desktop label.
func TestGnomeShellEvalProbeRequiresLiveAuthorizedPrivateBus(t *testing.T) {
	_, address, provider := startSyntheticGNOMEEvalBus(t)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	got := checkGnomeShellEval(rt, compositor.GNOME)
	if !got.Available || got.Name != "gnome-shell-eval" || !strings.Contains(got.Reason, "unsafe mode") {
		t.Fatalf("usable private GNOME shell not advertised: %+v", got)
	}
	provider.mu.Lock()
	if len(provider.scripts) != 1 || provider.scripts[0] != `"ok"` {
		t.Errorf("probe did not test exact Shell.Eval authority: %v", provider.scripts)
	}
	provider.failure = true
	provider.mu.Unlock()
	got = checkGnomeShellEval(rt, compositor.GNOME)
	if got.Available || !strings.Contains(got.Reason, "Shell Eval not available") {
		t.Fatalf("revoked Shell.Eval still advertised: %+v", got)
	}
}

// An actual private Shell.Eval service is the fallback authorization for
// GNOME when the native extension is absent; it must not select X11.
func TestGnomeRuntimePrivateShellEvalFallbackWithoutNativeBridge(t *testing.T) {
	_, address, provider := startSyntheticGNOMEEvalBus(t)
	rt := env.FromEnviron([]string{
		"DBUS_SESSION_BUS_ADDRESS=" + address,
		"WAYLAND_DISPLAY=/nonexistent/perfuncted-gnome-probe-socket",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DISPLAY=:999",
	})
	manager, err := openGNOMERuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatalf("usable authorized GNOME Shell.Eval fallback rejected: %v", err)
	}
	defer manager.Close()
	if _, ok := manager.(*GnomeManager); !ok {
		t.Fatalf("unexpected GNOME fallback backend %T", manager)
	}
	provider.mu.Lock()
	calls := len(provider.scripts)
	provider.mu.Unlock()
	if calls == 0 {
		t.Fatal("GNOME fallback never probed Shell.Eval")
	}
}
