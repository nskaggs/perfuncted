package input

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRemoteDesktopRejectsUnavailableStartupInputs(t *testing.T) {
	base := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=not-a-dbus-address",
	})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		rt   env.Runtime
		want error
	}{
		{name: "nil context", rt: base, want: ErrRemoteDesktopUnavailable},
		{name: "canceled context", ctx: canceled, rt: base, want: context.Canceled},
		{name: "missing Wayland display", ctx: context.Background(), rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=not-a-dbus-address"}), want: ErrRemoteDesktopUnavailable},
		{name: "invalid D-Bus address", ctx: context.Background(), rt: base, want: ErrRemoteDesktopUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputter, err := OpenRemoteDesktopContext(tc.ctx, tc.rt, RemoteDesktopOptions{})
			if inputter != nil {
				t.Fatalf("unavailable startup returned inputter %T", inputter)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("OpenRemoteDesktopContext error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSyntheticPortalStartupFailuresCloseAcquiredSession(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	baseRuntime := func() env.Runtime {
		return env.FromEnviron([]string{
			"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
			"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
			"XDG_STATE_HOME=" + t.TempDir(),
		})
	}
	testCases := []struct {
		name      string
		configure func(*testing.T)
		options   RemoteDesktopOptions
		want      error
		contains  string
	}{
		{
			name: "device authorization omitted",
			configure: func(*testing.T) {
				portal.startResponse = map[string]dbus.Variant{}
			},
			want: ErrRemoteDesktopDenied,
		},
		{
			name: "device authorization malformed",
			configure: func(*testing.T) {
				portal.startResponse = map[string]dbus.Variant{"devices": dbus.MakeVariant("keyboard and pointer")}
			},
			want: ErrRemoteDesktopDenied,
		},
		{
			name: "persistent permission omitted",
			configure: func(*testing.T) {
				portal.startResponse = map[string]dbus.Variant{"devices": dbus.MakeVariant(portalDeviceKeyboard | portalDevicePointer)}
			},
			options: RemoteDesktopOptions{PersistPermission: true},
			want:    ErrRemoteDesktopDenied,
		},
		{
			name: "persistent permission malformed",
			configure: func(*testing.T) {
				portal.startResponse = map[string]dbus.Variant{
					"devices":       dbus.MakeVariant(portalDeviceKeyboard | portalDevicePointer),
					"restore_token": dbus.MakeVariant(uint32(7)),
				}
			},
			options: RemoteDesktopOptions{PersistPermission: true},
			want:    ErrRemoteDesktopDenied,
		},
		{
			name: "EIS connection denied",
			configure: func(*testing.T) {
				portal.connectErr = dbus.MakeFailedError(errors.New("synthetic EIS refusal"))
			},
			want: ErrRemoteDesktopUnavailable,
		},
		{
			name: "EIS descriptor is not a socket",
			configure: func(t *testing.T) {
				_, writeEnd, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = writeEnd.Close() })
				portal.eisFD = dbus.UnixFD(writeEnd.Fd())
			},
			want:     ErrRemoteDesktopUnavailable,
			contains: "connect EIS descriptor",
		},
		{
			name: "EIS handshake rejected",
			configure: func(t *testing.T) {
				client, peer := unixConnectionPair(t)
				file, err := client.File()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = file.Close() })
				t.Cleanup(func() { _ = client.Close() })
				t.Cleanup(func() { _ = peer.Close() })
				portal.eisFD = dbus.UnixFD(file.Fd())
				if _, err := peer.Write(eiRequest(0, 0, eiU32(0))); err != nil {
					t.Fatal(err)
				}
			},
			contains: "server advertised handshake version zero",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			beforeClose := 0
			func() {
				portal.mu.Lock()
				defer portal.mu.Unlock()
				portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
				portal.persistToken = ""
				portal.startResponse = nil
				portal.connectErr = nil
				portal.eisFD = 0
				beforeClose = portal.closedSessions
				tc.configure(t)
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			inputter, err := OpenRemoteDesktopContext(ctx, baseRuntime(), tc.options)
			if inputter != nil {
				t.Fatalf("failed startup returned inputter %T", inputter)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("OpenRemoteDesktopContext error = %v, want %v", err, tc.want)
			}
			if tc.contains != "" && (err == nil || !strings.Contains(err.Error(), tc.contains)) {
				t.Fatalf("OpenRemoteDesktopContext error = %v, want %q", err, tc.contains)
			}
			if err == nil {
				t.Fatal("invalid portal grant unexpectedly opened an input backend")
			}
			portal.mu.Lock()
			closedSessions := portal.closedSessions
			portal.mu.Unlock()
			if closedSessions != beforeClose+1 {
				t.Fatalf("failed startup closed %d portal sessions, want exactly one", closedSessions-beforeClose)
			}
		})
	}
}
