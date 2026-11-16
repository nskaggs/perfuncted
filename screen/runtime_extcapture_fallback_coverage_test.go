package screen

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeSelectsExtCaptureForUnavailablePrimaryBackends(t *testing.T) {
	for _, tc := range []struct {
		name        string
		desktop     string
		additional  []extConstructorAdvertisedGlobal
		connections int
		needsBus    bool
	}{
		{
			name:        "KDE falls back after unavailable KWin",
			desktop:     "KDE",
			additional:  []extConstructorAdvertisedGlobal{{25, "org_kde_plasma_shell", 1}},
			connections: 2,
			needsBus:    true,
		},
		{
			name:        "wlroots falls back after unavailable screencopy",
			desktop:     "other",
			additional:  []extConstructorAdvertisedGlobal{{25, "zwlr_screencopy_manager_v1", 1}},
			connections: 3,
		},
		{
			name:        "unknown Wayland falls back after unavailable screencopy",
			desktop:     "other",
			connections: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				for index := 0; index < tc.connections; index++ {
					conn, acceptErr := listener.AcceptUnix()
					if acceptErr != nil {
						serverDone <- fmt.Errorf("accept Wayland connection %d: %w", index+1, acceptErr)
						return
					}
					var additional []extConstructorAdvertisedGlobal
					if index == 0 {
						additional = tc.additional
					}
					if err := serveExtConstructorPeerWithGlobals(conn, "", additional); err != nil {
						serverDone <- fmt.Errorf("Wayland connection %d: %w", index+1, err)
						return
					}
				}
				serverDone <- nil
			}()

			vars := []string{
				"WAYLAND_DISPLAY=" + socket,
				"XDG_CURRENT_DESKTOP=" + tc.desktop,
			}
			if tc.needsBus {
				vars = append(vars, "DBUS_SESSION_BUS_ADDRESS=unix:path="+filepath.Join(t.TempDir(), "absent-bus"))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			backend, err := OpenRuntimeContext(ctx, env.FromEnviron(vars))
			if err != nil {
				t.Fatalf("runtime did not select available ExtCapture backend: %v", err)
			}
			if _, ok := backend.(*ExtCaptureBackend); !ok {
				t.Fatalf("runtime selected %T, want ExtCaptureBackend", backend)
			}
			if err := backend.Close(); err != nil {
				t.Fatalf("close selected backend: %v", err)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("synthetic compositor: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("runtime backend did not release its Wayland sessions")
			}
		})
	}
}
