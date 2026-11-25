package input

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
			stateDir := privatePortalTokenFixtureDir(t)
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

func TestSyntheticPortalPersistentGrantConsumesAndRotatesRestoreToken(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	stateDir := privatePortalTokenFixtureDir(t)
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

// Even unsupported queries must report their operation lifetime truthfully:
// canceled callers and revoked authorization take precedence over capability.
func TestRemoteDesktopPointerLocationHonorsCancellationAndAuthorization(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"canceled", ctx, context.Canceled},
		{"live unsupported", context.Background(), ErrNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, y, err := b.PointerLocation(tc.ctx)
			if x != 0 || y != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("position (%d,%d) error %v, want %v", x, y, err, tc.want)
			}
		})
	}
	if _, _, err := b.PointerLocation(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := b.revoke(errors.New("synthetic authorization retired")); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revocation should report lost authorization, got %v", err)
	}
	if x, y, err := b.PointerLocation(context.Background()); x != 0 || y != 0 || !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revoked pointer query (%d,%d): %v", x, y, err)
	}
}

// An already-canceled caller must not enter the portal command queue even
// when its unbuffered receiver is ready at the same instant. Select alone
// does not give canceled contexts priority over other ready channels.
func TestRemoteDesktopPreCanceledOperationNeverEntersReadyCommandQueue(t *testing.T) {
	for i := 0; i < 128; i++ {
		b := syntheticQueuedPortal()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		receiverReady := make(chan struct{})
		stopReceiver := make(chan struct{})
		completed := make(chan struct{})
		var accepted atomic.Bool
		go func() {
			defer close(completed)
			close(receiverReady)
			select {
			case cmd := <-b.commands:
				accepted.Store(true)
				cmd.done <- nil
			case <-stopReceiver:
			}
		}()
		<-receiverReady
		err := b.submit(ctx, func() error { return nil })
		close(stopReceiver)
		<-completed
		if accepted.Load() {
			t.Fatalf("iteration %d: canceled caller entered the ready command queue", i)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: expected cancellation, got %v", i, err)
		}
	}
}

// Canceled portal operations lose authority before validating text, mouse
// coordinates or button numbers. Invalid arguments may not supersede the
// cancellation, and none of these operations may enqueue EIS frames.
func TestRemoteDesktopPreCanceledPointerAndLiteralPreflight(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"literal invalid UTF8", func(ctx context.Context) error { return b.TypeLiteral(ctx, string([]byte{0xff})) }},
		{"mouse move invalid region", func(ctx context.Context) error { return b.MouseMove(ctx, -999, -999) }},
		{"mouse click invalid button", func(ctx context.Context) error { return b.MouseClick(ctx, -999, -999, 19) }},
		{"mouse down invalid button", func(ctx context.Context) error { return b.MouseDown(ctx, 19) }},
		{"mouse up invalid button", func(ctx context.Context) error { return b.MouseUp(ctx, 19) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation returned %v", err)
			}
			if err := tc.call(nil); err == nil || err.Error() != "input/remote-desktop: nil operation context" {
				t.Fatalf("nil context returned %v", err)
			}
		})
	}
}
