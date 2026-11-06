package input

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
)

// Startup is a series of authorization checkpoints, not a best-effort
// fallback. Each failed checkpoint must reject the session, and any session
// already acquired from the trusted portal service must be explicitly closed.
func TestSyntheticPortalStartupStageFailuresDoNotExposeInputAuthority(t *testing.T) { //nolint:gocyclo // Individual authorization checkpoints are one explicitly verified cleanup matrix.
	for _, tc := range []struct {
		name      string
		configure func(*testing.T, *syntheticPortal, env.Runtime)
		persist   bool
		want      error
		message   string
		attempts  int
		closes    int
	}{
		{name: "unsupported portal version", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.getVersion = uint32(1) }, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "wrong version type", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.getVersion = "version 2" }, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "version property read fails", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) {
			p.versionFailure = "synthetic properties failure"
		}, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "insecure restore token directory", configure: func(t *testing.T, _ *syntheticPortal, rt env.Runtime) {
			private := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
			if err := os.MkdirAll(private, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(private, 0777); err != nil {
				t.Fatal(err)
			}
		}, persist: true, message: "secure restore-token store", attempts: 0, closes: 0},
		{name: "malformed saved restore token", configure: func(t *testing.T, _ *syntheticPortal, rt env.Runtime) {
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
			if err := os.WriteFile(path, []byte("untrusted credential without newline"), 0600); err != nil {
				t.Fatal(err)
			}
		}, persist: true, message: "consume restore token", attempts: 0, closes: 0},
		{name: "create session rejected", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.createFailure = "synthetic create failed" }, want: ErrRemoteDesktopUnavailable, attempts: 1, closes: 0},
		{name: "invalid session handle", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) {
			p.result = map[string]dbus.Variant{"session_handle": dbus.MakeVariant("")}
		}, want: ErrRemoteDesktopUnavailable, message: "invalid session handle", attempts: 1, closes: 0},
		{name: "select devices denied", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.selectCode = 1 }, want: ErrRemoteDesktopDenied, attempts: 1, closes: 1},
		{name: "start denied", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.startCode = 1 }, want: ErrRemoteDesktopDenied, attempts: 1, closes: 1},
		{name: "missing pointer authority", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.deviceMask = portalDeviceKeyboard }, want: ErrRemoteDesktopDenied, message: "did not authorize keyboard and pointer", attempts: 1, closes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, portal := startSyntheticPortalBus(t)
			root := privatePortalTokenFixtureDir(t)
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/tmp/pf-test-fake-wayland.sock",
				"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
				"XDG_STATE_HOME=" + root,
			})
			portal.mu.Lock()
			if tc.configure != nil {
				tc.configure(t, portal, rt)
			}
			portal.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			backend, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: tc.persist})
			if backend != nil || err == nil {
				if backend != nil {
					_ = backend.Close()
				}
				t.Fatalf("failed checkpoint granted input: backend=%v err=%v", backend, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("checkpoint err=%v, want %v", err, tc.want)
			}
			if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("checkpoint err=%v, want %q", err, tc.message)
			}
			portal.mu.Lock()
			requests, closes := portal.called, portal.closedSessions
			portal.mu.Unlock()
			if requests != tc.attempts || closes != tc.closes {
				t.Fatalf("resource ownership mismatch: createRequests=%d, closeCalls=%d, want %d,%d; error=%v", requests, closes, tc.attempts, tc.closes, err)
			}
		})
	}
}
