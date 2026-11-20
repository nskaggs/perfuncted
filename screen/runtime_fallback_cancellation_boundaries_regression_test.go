package screen

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestScreenRuntimeStopsFallbackWhenExtCaptureSetupIsCanceled(t *testing.T) {
	for _, tc := range []struct {
		name        string
		desktop     string
		globals     []extConstructorAdvertisedGlobal
		connections int
	}{
		{"KDE", "KDE", []extConstructorAdvertisedGlobal{{25, "org_kde_plasma_shell", 1}}, 2},
		{"wlroots", "Sway", []extConstructorAdvertisedGlobal{{25, "zwlr_screencopy_manager_v1", 1}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := shortPrivateSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				for i := 0; i < tc.connections; i++ {
					conn, err := listener.AcceptUnix()
					if err != nil {
						done <- err
						return
					}
					if i == tc.connections-1 {
						done <- cancelExtCaptureRegistryRoundTrip(conn, cancel)
						return
					}
					var globals []extConstructorAdvertisedGlobal
					if i == 0 {
						globals = tc.globals
					}
					if err := serveExtConstructorPeerWithGlobals(conn, "", globals); err != nil {
						done <- fmt.Errorf("serve Wayland connection %d: %w", i+1, err)
						return
					}
				}
			}()
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=" + socket,
				"XDG_CURRENT_DESKTOP=" + tc.desktop,
				"DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(t.TempDir(), "missing"),
			})
			backend, err := OpenRuntimeContext(ctx, rt)
			if backend != nil || !errors.Is(err, context.Canceled) {
				if backend != nil {
					_ = backend.Close()
				}
				t.Fatalf("canceled ext-capture setup returned %T, %v", backend, err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled Wayland setup did not close")
			}
		})
	}
}

type cancelKWinScreenProbe struct {
	cancel  context.CancelFunc
	started chan struct{}
	release chan struct{}
}

func (s *cancelKWinScreenProbe) CaptureArea(_ int32, _ int32, _ uint32, _ uint32, _ map[string]dbus.Variant, fd dbus.UnixFD) (map[string]dbus.Variant, *dbus.Error) {
	file := os.NewFile(uintptr(fd), "kwin-probe")
	defer file.Close()
	close(s.started)
	s.cancel()
	<-s.release
	return nil, dbus.MakeFailedError(errors.New("probe canceled"))
}

func TestScreenRuntimeStopsFallbackWhenKWinProbeIsCanceled(t *testing.T) {
	_, bus := newSyntheticScreenshotPortal(t)
	reply, err := bus.conn.RequestName(kwinShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim KWin service: reply=%v err=%v", reply, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	probe := &cancelKWinScreenProbe{cancel: cancel, started: make(chan struct{}), release: make(chan struct{})}
	defer close(probe.release)
	if err := bus.conn.Export(probe, dbus.ObjectPath(kwinShotPath), kwinShotIface); err != nil {
		t.Fatal(err)
	}
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=KDE",
		"DBUS_SESSION_BUS_ADDRESS=" + bus.busAddress,
	})
	backend, err := OpenRuntimeContext(ctx, rt)
	if backend != nil || !errors.Is(err, context.Canceled) {
		if backend != nil {
			_ = backend.Close()
		}
		t.Fatalf("canceled KWin probe returned %T, %v", backend, err)
	}
	select {
	case <-probe.started:
	default:
		t.Fatal("KWin authorization probe was not reached")
	}
}
