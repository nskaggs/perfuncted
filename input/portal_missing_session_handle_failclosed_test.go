package input

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
)

// A successful D-Bus response without a session handle is not an authorized
// session. The missing handle must abort before any input devices are selected.
func TestRemoteDesktopRejectsSuccessfulPortalResponseWithoutSessionHandle(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	portal.mu.Lock()
	portal.result = map[string]dbus.Variant{}
	portal.mu.Unlock()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/perfuncted-test-portal-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	backend, err := OpenRemoteDesktopContext(ctx, runtime, RemoteDesktopOptions{})
	if backend != nil || !errors.Is(err, ErrRemoteDesktopUnavailable) || !strings.Contains(err.Error(), "omitted session handle") {
		t.Fatalf("portal session handle omission was accepted: backend=%T err=%v", backend, err)
	}
	portal.mu.Lock()
	creates, closes := portal.called, portal.closedSessions
	portal.mu.Unlock()
	if creates != 1 || closes != 0 {
		t.Fatalf("incorrect unacquired session ownership: requests=%d closes=%d", creates, closes)
	}
}
