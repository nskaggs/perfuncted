//go:build linux

package compositor

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/testutil"
	"github.com/nskaggs/perfuncted/internal/wl"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Cancellation while the real Wayland registry is unresponsive must win over
// stale desktop authority; the probe must close its socket and return promptly.
func TestCompositorCancellationDuringRegistryProbeFailsClosed(t *testing.T) {
	dir := testutil.ShortSocketDir(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "display.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	connected := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		close(connected)
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var b [1]byte
		for {
			if _, err := conn.Read(b[:]); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + dir, "WAYLAND_DISPLAY=display.sock", "XDG_CURRENT_DESKTOP=KDE"})
	result := make(chan struct {
		kind Session
		err  error
	}, 1)
	go func() {
		kind, err := DetectRuntimeContext(ctx, rt)
		result <- struct {
			kind Session
			err  error
		}{kind, err}
	}()

	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("Wayland registry probe did not connect")
	}
	cancel()
	select {
	case got := <-result:
		if got.kind != Unknown || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled registry probe selected %v with err %v", got.kind, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled registry probe did not return")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled registry probe did not close socket")
	}
}

func TestKWinDetectionRequiresExactBusServiceName(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("synthetic dbus-daemon unavailable")
	}
	address := "unix:path=" + filepath.Join(testutil.ShortSocketDir(t), "bus")
	proc := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+address)
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var owner *dbus.Conn
	var lastErr error
	for ctx.Err() == nil {
		var err error
		owner, err = dbusutil.SessionBusAddressContext(ctx, address)
		if err == nil {
			break
		}
		lastErr = err
		time.Sleep(5 * time.Millisecond)
	}
	if owner == nil {
		t.Fatalf("private D-Bus session not ready: %v", lastErr)
	}
	defer owner.Close()
	reply, err := owner.RequestName("org.kde.KWinSpoof", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register impostor service: %v %v", reply, err)
	}
	if kwinOnBus(address) {
		t.Fatal("KWin detection accepted unrelated bus name with KWin prefix")
	}
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=perfuncted-missing-socket", "DBUS_SESSION_BUS_ADDRESS=" + address})
	if got := DetectRuntime(rt); got != Unknown {
		t.Fatalf("impostor KWin service selected %v", got)
	}
	reply, err = owner.RequestName("org.kde.KWin", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register genuine KWin service: %v %v", reply, err)
	}
	if !kwinOnBus(address) {
		t.Fatal("exact KWin service not detected")
	}
	if got := DetectRuntime(rt); got != KDE {
		t.Fatalf("genuine KWin service selected %v", got)
	}
}

// Detect against a private real Wayland registry exchange rather than relying
// on desktop environment labels (which can be stale in nested sessions).
func TestDetectRuntimeRealCompositorGlobalsOverrideMisleadingDesktop(t *testing.T) {
	for _, tc := range []struct {
		name       string
		interfaces []string
		desktop    string
		want       Session
	}{
		{"wlroots beats KDE", []string{"org_kde_plasma_shell", "zwlr_screencopy_manager_v1"}, "KDE", Wlroots},
		{"KDE beats GNOME", []string{"gtk_shell1", "org_kde_plasma_shell"}, "GNOME", KDE},
		{"GNOME gtk shell", []string{"gtk_shell1"}, "KDE", GNOME},
		{"GNOME gtk surface", []string{"gtk_surface1"}, "SWAY", GNOME},
		{"wlroots virtual keyboard", []string{"zwlr_virtual_keyboard_manager_v1"}, "KDE", Wlroots},
		{"wlroots virtual pointer", []string{"zwlr_virtual_pointer_manager_v1"}, "GNOME", Wlroots},
		{"wlroots toplevel", []string{"zwlr_foreign_toplevel_manager_v1"}, "KDE", Wlroots},
		{"KDE alternate prefix", []string{"kde_plasma_shell"}, "GNOME", KDE},
		{"shared KDE decoration is not KWin", []string{"org_kde_kwin_server_decoration_manager"}, "KDE", Unknown},
		{"shared decoration and filtered wlroots globals", []string{"wl_compositor", "org_kde_kwin_server_decoration_manager"}, "SWAY", Wlroots},
		{"unrecognized protocol", []string{"wl_output", "wl_seat"}, "KDE", Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := testutil.ShortSocketDir(t)
			socket := filepath.Join(dir, "display.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			peer := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					peer <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
				registry := uint32(0)
				for {
					var header [8]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						if errors.Is(err, io.EOF) {
							peer <- nil
						} else {
							peer <- err
						}
						return
					}
					size := int(wl.Uint32(header[4:]) >> 16)
					if size < 8 || size > 4096 {
						peer <- fmt.Errorf("invalid compositor wire request %d", size)
						return
					}
					body := make([]byte, size-8)
					if _, err := io.ReadFull(conn, body); err != nil {
						peer <- err
						return
					}
					opcode, sender := wl.Uint32(header[4:])&0xffff, wl.Uint32(header[:4])
					if sender != 1 {
						peer <- fmt.Errorf("unexpected sender %d opcode %d", sender, opcode)
						return
					}
					switch opcode {
					case 1:
						registry = wl.Uint32(body)
					case 0:
						callback := wl.Uint32(body)
						for i, iface := range tc.interfaces {
							n := len(iface) + 1
							pad := (n + 3) &^ 3
							global := make([]byte, 8+pad+4)
							wl.PutUint32(global[:4], uint32(i+30))
							wl.PutUint32(global[4:8], uint32(n))
							copy(global[8:], iface)
							wl.PutUint32(global[8+pad:], 1)
							if err := writeCompositorEvent(conn, registry, 0, global); err != nil {
								peer <- err
								return
							}
						}
						if err := writeCompositorEvent(conn, callback, 0, make([]byte, 4)); err != nil {
							peer <- err
							return
						}
					default:
						peer <- fmt.Errorf("invalid display request opcode %d", opcode)
						return
					}
				}
			}()
			rt := env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + dir, "WAYLAND_DISPLAY=display.sock", "XDG_CURRENT_DESKTOP=" + tc.desktop})
			if got := DetectRuntime(rt); got != tc.want {
				t.Fatalf("real registry with %v detected %s, want %s", tc.interfaces, got, tc.want)
			}
			select {
			case err := <-peer:
				if err != nil {
					t.Fatalf("synthetic compositor failed: %v", err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("compositor connection leaked after probe")
			}
		})
	}
}
func writeCompositorEvent(conn *net.UnixConn, object, opcode uint32, body []byte) error {
	event := make([]byte, 8+len(body))
	wl.PutUint32(event[:4], object)
	wl.PutUint32(event[4:8], uint32(len(event))<<16|opcode)
	copy(event[8:], body)
	_, err := conn.Write(event)
	return err
}

func TestCompositorDetectionCanceledAndUnreachableSocketDoNotSelectDesktopAuthority(t *testing.T) {
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=unreachable", "XDG_CURRENT_DESKTOP=KDE"})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if kind, err := DetectRuntimeContext(canceled, rt); kind != Unknown || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled detection selected %s err=%v", kind, err)
	}
	if kind, ok := probeGlobalsContext(context.Background(), env.FromEnviron([]string{"WAYLAND_DISPLAY=", "XDG_RUNTIME_DIR="})); ok || kind != X11 {
		t.Fatalf("empty compositor socket detected: kind=%s ok=%v", kind, ok)
	}
	missing := env.FromEnviron([]string{"WAYLAND_DISPLAY=" + filepath.Join(t.TempDir(), "no-socket")})
	if kind, ok := probeGlobalsContext(context.Background(), missing); ok || kind != X11 {
		t.Fatalf("unreachable compositor accepted: kind=%s ok=%v", kind, ok)
	}
	deadline, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if kind, err := DetectRuntimeContext(deadline, missing); kind != Unknown || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired detection selected %s err=%v", kind, err)
	}
}

// A filtered compositor registry may omit privileged wlroots globals. Only
// explicit compositor session identifiers can authorize the wlroots fallback;
// vendor names containing a token must not be treated as an identity.
func TestWlrootsSessionHintRequiresExplicitIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars []string
		want bool
	}{
		{"sway socket", []string{"SWAYSOCK=/run/user/1000/sway.sock", "XDG_CURRENT_DESKTOP=KDE"}, true},
		{"hyprland instance", []string{"HYPRLAND_INSTANCE_SIGNATURE=session", "XDG_CURRENT_DESKTOP=GNOME"}, true},
		{"nested river", []string{"XDG_CURRENT_DESKTOP=ubuntu:RIVER"}, true},
		{"named wayfire variant", []string{"XDG_CURRENT_DESKTOP=WAYFIRE-Experimental"}, true},
		{"nested hyprland", []string{"XDG_CURRENT_DESKTOP=GNOME:Hyprland"}, true},
		{"vendor spoofed sway", []string{"XDG_CURRENT_DESKTOP=NOTSWAY"}, false},
		{"suffix spoofed hyprland", []string{"XDG_CURRENT_DESKTOP=HyprlandTools"}, false},
		{"generic kde", []string{"XDG_CURRENT_DESKTOP=KDE"}, false},
		{"empty desktop", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := env.FromEnviron(tc.vars)
			if got := wlrootsSessionHint(rt); got != tc.want {
				t.Fatalf("wlroots session hint %v = %v, want %v", tc.vars, got, tc.want)
			}
		})
	}
}
