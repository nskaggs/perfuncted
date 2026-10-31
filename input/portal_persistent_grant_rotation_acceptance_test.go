package input

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestSyntheticPortalPersistentGrantConsumesAndRotatesRestoreToken(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	stateDir := t.TempDir()
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/fixture-wayland",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
		"XDG_STATE_HOME=" + stateDir,
	})
	store, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.store("previous-grant"); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	eisClient, eisServer := unixConnectionPair(t)
	t.Cleanup(func() { _ = eisServer.Close() })
	file, err := eisClient.File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	_ = eisClient.Close()
	portal.mu.Lock()
	portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
	portal.persistToken = "rotated-grant"
	portal.eisFD = dbus.UnixFD(file.Fd())
	portal.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveSyntheticAuthorizedEISPeer(eisServer) }()
	inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: true})
	if err != nil {
		t.Fatalf("persistent portal authorization: %v", err)
	}
	backend, ok := inputter.(*RemoteDesktopBackend)
	if !ok {
		t.Fatalf("unexpected inputter type %T", inputter)
	}
	portal.mu.Lock()
	options := portal.selectOptions
	portal.mu.Unlock()
	if options["persist_mode"].Value() != portalPersistForever {
		t.Fatalf("persistent grant mode not requested: %+v", options)
	}
	if options["restore_token"].Value() != "previous-grant" {
		t.Fatalf("prior restore token not offered to the portal: %+v", options["restore_token"])
	}
	select {
	case peerErr := <-peerDone:
		if peerErr != nil {
			t.Fatalf("synthetic EIS handshake: %v", peerErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic EIS handshake did not finish: %v", ctx.Err())
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close authorized backend: %v", err)
	}

	path := filepath.Join(stateDir, "perfuncted", portalRestoreTokenFile)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("rotated token file mode: stat=%v err=%v", info, err)
	}
	rotated, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rotated.consume()
	closeErr := rotated.Close()
	if err != nil || closeErr != nil || got != "rotated-grant" {
		t.Fatalf("stored restore token = %q, consume err=%v close err=%v", got, err, closeErr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consuming rotated token did not remove its file: %v", err)
	}
}
