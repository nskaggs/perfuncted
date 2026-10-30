package input

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestSyntheticPortalPersistentGrantRequiresValidRestoreToken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{"missing persistent grant", "", "did not grant persistent permission"},
		{"embedded NUL cannot pass D-Bus", "bad\x00token", "context deadline exceeded"},
		{"newline", "bad\ntoken", "malformed restore token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := startSyntheticPortalBus(t)
			p.mu.Lock()
			p.deviceMask = portalDeviceKeyboard | portalDevicePointer
			p.persistToken = tc.token
			p.mu.Unlock()
			stateDir := t.TempDir()
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/tmp/fixture-wayland",
				"DBUS_SESSION_BUS_ADDRESS=" + p.busAddress,
				"XDG_STATE_HOME=" + stateDir,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
			defer cancel()
			backend, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: true})
			if backend != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("persistent grant %q => backend=%v err=%v want %q", tc.name, backend, err, tc.want)
			}
			tokenPath := filepath.Join(stateDir, "perfuncted", portalRestoreTokenFile)
			if _, err := os.Stat(tokenPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid restore token was persisted: %v", err)
			}
			p.mu.Lock()
			options := p.selectOptions
			p.mu.Unlock()
			if options["persist_mode"].Value() != portalPersistForever {
				t.Fatalf("persistent authorization was not explicitly requested: %+v", options)
			}
		})
	}
}

func TestPortalRestoreTokenStoreNeverUsesUntrustedRelativeStatePath(t *testing.T) {
	rt := env.FromEnviron([]string{
		"XDG_STATE_HOME=relative-state",
		"WAYLAND_DISPLAY=/tmp/test-wayland",
	})
	_, err := OpenRemoteDesktopContext(context.Background(), rt, RemoteDesktopOptions{PersistPermission: true})
	// Startup must fail early, and never silently write permission state in cwd.
	if err == nil {
		t.Fatal("remote input unexpectedly authorized without a session bus")
	}
	if _, err := lockPortalTokenStore(rt); err == nil {
		t.Fatal("relative token-store path accepted")
	}
}
