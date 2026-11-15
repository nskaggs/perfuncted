//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A GNOME bridge cannot authorize current-session automation when extension
// refresh failed, the session bus was absent, or an unavailable bridge could
// not be installed. Preserve the original failures and close stale clients.
func TestConnectRuntimeFailClosedRefreshAndInstallBoundaries(t *testing.T) {
	unavailable := errors.New("synthetic extension installation denied")
	bus := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/private-bus"})
	obsolete := &Client{extensionVer: "0"}
	installCount := 0
	got, err := connectRuntime(nil, bus,
		func(ctx context.Context, address string) (*Client, error) {
			if ctx == nil || address != "unix:path=/private-bus" {
				t.Fatalf("nil fallback context or wrong address: %v %q", ctx, address)
			}
			return obsolete, nil
		},
		func(context.Context, env.Runtime) (string, error) { installCount++; return "", unavailable },
	)
	if got != nil || !errors.Is(err, ErrUnavailable) || !errors.Is(err, unavailable) || !obsolete.closed || installCount != 1 {
		t.Fatalf("obsolete bridge refresh admitted: client=%v err=%v closed=%v installs=%d", got, err, obsolete.closed, installCount)
	}
	t.Run("no session bus", func(t *testing.T) {
		installs := 0
		got, err := connectRuntime(context.Background(), env.FromEnviron(nil),
			func(context.Context, string) (*Client, error) { return nil, ErrUnavailable },
			func(context.Context, env.Runtime) (string, error) { installs++; return "", nil },
		)
		if got != nil || !errors.Is(err, ErrUnavailable) || installs != 0 {
			t.Fatalf("bridge installed without bus: client=%v err=%v installs=%d", got, err, installs)
		}
	})
	t.Run("installation refused", func(t *testing.T) {
		installs := 0
		got, err := connectRuntime(context.Background(), bus,
			func(context.Context, string) (*Client, error) { return nil, ErrUnavailable },
			func(context.Context, env.Runtime) (string, error) { installs++; return "", unavailable },
		)
		if got != nil || !errors.Is(err, ErrUnavailable) || !errors.Is(err, unavailable) || installs != 1 {
			t.Fatalf("failed install authorized bridge: client=%v err=%v installs=%d", got, err, installs)
		}
	})
}
