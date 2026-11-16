//go:build linux

package input

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A portal grant cannot escape startup unless its persistent token is stored
// atomically; a failed replacement must also close the acquired session.
func TestOpenRemoteDesktopRejectsAndCleansFailedPersistentTokenRotation(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	root := privatePortalTokenFixtureDir(t)
	tokenDir := filepath.Join(root, "state", "perfuncted")
	target := filepath.Join(tokenDir, portalRestoreTokenFile)
	hookResult := make(chan error, 1)
	portal.mu.Lock()
	portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
	portal.persistToken = "new-authorized-grant"
	portal.startHook = func() { hookResult <- os.Mkdir(target, 0700) }
	portal.mu.Unlock()
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
		"XDG_STATE_HOME=" + filepath.Join(root, "state"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: true})
	if inputter != nil {
		_ = inputter.Close()
		t.Fatalf("failed persistent token rotation returned inputter %T", inputter)
	}
	if err == nil || !strings.Contains(err.Error(), "persist restore token securely") {
		t.Fatalf("token rotation failure = %v, want secure persistence error", err)
	}
	if hookErr := <-hookResult; hookErr != nil {
		t.Fatalf("create directory at token destination: %v", hookErr)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("failed replacement changed destination: stat=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(tokenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".restore-token-") {
			t.Fatalf("failed replacement left temporary grant %q", entry.Name())
		}
	}
	portal.mu.Lock()
	calls, closes := portal.called, portal.closedSessions
	portal.mu.Unlock()
	if calls != 1 || closes != 1 {
		t.Fatalf("startup failure acquired or retained unexpected portal resources: create calls=%d closes=%d", calls, closes)
	}
}
