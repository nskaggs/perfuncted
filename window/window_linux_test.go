//go:build linux

package window

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/internal/testutil"
	"github.com/nskaggs/perfuncted/internal/x11test"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenRuntimeUsesToplevelBackendForUnknownExtWayland(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 2)
	go func() {
		for range 2 {
			conn, err := listener.AcceptUnix()
			if err != nil {
				done <- err
				return
			}
			done <- serveWindowCompositor(conn, "ext")
		}
	}()

	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=unrecognized",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	manager, err := OpenRuntimeContext(ctx, rt)
	if err != nil {
		t.Fatalf("open unknown ext-toplevel Wayland runtime: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = manager.Close()
		}
	})
	if _, ok := manager.(*WaylandWindowManager); !ok {
		t.Fatalf("unknown ext-toplevel compositor selected %T, want WaylandWindowManager", manager)
	}
	windows, err := manager.List(ctx)
	if err != nil || len(windows) != 1 || windows[0].Title != "Editor" || windows[0].NativeID != "stable-editor-7" {
		t.Fatalf("fallback window snapshot=%+v err=%v", windows, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close fallback manager: %v", err)
	}
	closed = true
	for range 2 {
		select {
		case serverErr := <-done:
			if serverErr != nil {
				t.Fatalf("synthetic compositor: %v", serverErr)
			}
		case <-ctx.Done():
			t.Fatal("runtime did not release both Wayland connections")
		}
	}
}

func TestOpenRuntimeContextSelectsLiveX11WindowManager(t *testing.T) {
	authority := filepath.Join(t.TempDir(), "empty-xauthority")
	if err := os.WriteFile(authority, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", authority)
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("isolated Xvfb unavailable: %v", err)
	}
	t.Cleanup(stop)

	manager, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=" + display}))
	if err != nil {
		t.Fatalf("open window runtime on Xvfb: %v", err)
	}
	x11Manager, ok := manager.(*X11Backend)
	if !ok || x11Manager == nil {
		t.Fatalf("window runtime returned %T, want live *X11Backend", manager)
	}
	t.Cleanup(func() { _ = manager.Close() })

	windows, err := manager.List(context.Background())
	if err != nil {
		t.Fatalf("list Xvfb windows: %v", err)
	}
	if len(windows) != 0 {
		t.Fatalf("fresh Xvfb unexpectedly contains windows: %+v", windows)
	}
}

// Real D-Bus/KWin transport overrides stale session labels only where the
// compositor selection rules authorize it. No actual user desktop is touched.
func TestWindowRuntimeKDESyntheticDBusAndFailClosedSelections(t *testing.T) {
	_, provider := newSyntheticKWinManager(t)
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/nonexistent/perfuncted-wayland-test",
		"XDG_CURRENT_DESKTOP=plasma:KDE",
		"DBUS_SESSION_BUS_ADDRESS=" + provider.busAddress,
	})
	manager, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatalf("KDE runtime failed to open real private-bus KWin service: %v", err)
	}
	defer manager.Close()
	if _, ok := manager.(*KWinScriptManager); !ok {
		t.Fatalf("KDE runtime selected %T instead of KWin scripting", manager)
	}
	infos, err := manager.List(context.Background())
	if err != nil || len(infos) != 1 || infos[0].NativeID != "7" || infos[0].PID != 33 {
		t.Fatalf("KDE runtime list %+v %v", infos, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.List(context.Background()); err == nil {
		t.Fatal("closed KDE manager fabricated data")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if m, err := OpenRuntimeContext(canceled, rt); m != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled KDE runtime unexpectedly opened: %T %v", m, err)
	}
}
func TestWindowRuntimeFailClosedWhenRequestedCompositorIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars []string
		part string
	}{
		{"KDE without bus", []string{"WAYLAND_DISPLAY=/tmp/perfuncted-invalid-display", "XDG_CURRENT_DESKTOP=KDE", "DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-bus"}, "KDE detected"},
		{"wlroots unavailable", []string{"WAYLAND_DISPLAY=/tmp/perfuncted-invalid-display", "XDG_CURRENT_DESKTOP=sway", "SWAYSOCK=/nonexistent/perfuncted-sway"}, "wlroots"},
		{"X11 no display", []string{"XDG_SESSION_TYPE=x11"}, "no display"},
		{"unknown Wayland", []string{"WAYLAND_DISPLAY=/tmp/perfuncted-invalid-display", "XDG_CURRENT_DESKTOP=unrecognized"}, "unsupported Wayland"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, err := OpenRuntimeContext(context.Background(), env.FromEnviron(tc.vars))
			if manager != nil || err == nil || !strings.Contains(err.Error(), tc.part) {
				t.Fatalf("unavailable %s opened manager %T, error %v", tc.name, manager, err)
			}
		})
	}
}

type privateGNOMEMultipleWindows struct{}

func (privateGNOMEMultipleWindows) ListWindows() ([]gnomebridge.WindowInfo, *dbus.Error) {
	return []gnomebridge.WindowInfo{
		{ID: "17", Title: "Private Editor", AppID: "org.test.Editor", Width: 600, Height: 400},
		{ID: "18", Title: "Private Terminal", AppID: "org.test.Terminal", Width: 800, Height: 600},
	}, nil
}

func (privateGNOMEMultipleWindows) GetActiveWindow() (gnomebridge.WindowInfo, *dbus.Error) {
	return gnomebridge.WindowInfo{ID: "17", Title: "Private Editor", Active: true}, nil
}

func (privateGNOMEMultipleWindows) GetWindow(id string) (gnomebridge.WindowInfo, *dbus.Error) {
	return gnomebridge.WindowInfo{ID: id, Title: "Private Editor"}, nil
}

func TestOpenRuntimeSelectsGNOMENativeManagerAndIteratorHonorsStop(t *testing.T) {
	_, address, _ := startSyntheticGNOMEEvalBus(t)
	conn, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if reply, nameErr := conn.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue); nameErr != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim private bridge name: %v %v", nameErr, reply)
	}
	path := dbus.ObjectPath(gnomebridge.ObjectPath)
	if err := conn.Export(privateGNOMECore{}, path, gnomebridge.CoreInterface); err != nil {
		t.Fatal(err)
	}
	if err := conn.Export(privateGNOMEMultipleWindows{}, path, gnomebridge.WindowsInterface); err != nil {
		t.Fatal(err)
	}

	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/nonexistent/pf-window-runtime.sock",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
	})
	manager, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatalf("open GNOME runtime through its private native bridge: %v", err)
	}
	defer manager.Close()
	if _, ok := manager.(*GnomeNativeManager); !ok {
		t.Fatalf("GNOME runtime selected %T, want native manager", manager)
	}

	visited := 0
	for info, err := range manager.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatalf("iterate native GNOME windows: %v", err)
		}
		if info.ID != 17 || info.Title != "Private Editor" {
			t.Fatalf("first native GNOME window = %+v", info)
		}
		visited++
		break
	}
	if visited != 1 {
		t.Fatalf("iterator yielded %d windows after caller stopped, want 1", visited)
	}
}

func TestWindowRuntimePrefersSwayIPCOnWlroots(t *testing.T) {
	dir := testutil.ShortSocketDir(t)
	sock := filepath.Join(dir, "sway.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		kind, payload, err := readSwayMessage(conn)
		if err == nil && (kind != swayMsgGetTree || len(payload) != 0) {
			err = fmt.Errorf("unexpected Sway probe: kind=%d payload=%q", kind, payload)
		}
		if err == nil {
			err = writeSwayMessage(conn, swayMsgGetTree, syntheticSwayWindowTree)
		}
		done <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + filepath.Join(dir, "unavailable-wayland"),
		"SWAYSOCK=" + sock,
	})
	manager, err := OpenRuntimeContext(ctx, rt)
	if err != nil {
		t.Fatalf("open Sway runtime: %v", err)
	}
	defer manager.Close()
	sway, ok := manager.(*SwayManager)
	if !ok || sway.sock != sock {
		t.Fatalf("runtime selected %T, want Sway on %q", manager, sock)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Sway IPC probe: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Sway probe did not finish")
	}
}

func TestWindowRuntimeFallsBackToWaylandWhenSwayIPCUnavailable(t *testing.T) {
	dir := testutil.ShortSocketDir(t)
	sock := filepath.Join(dir, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		for range 2 {
			conn, err := listener.AcceptUnix()
			if err != nil {
				done <- err
				return
			}
			if err := serveWindowCompositor(conn, "wlr"); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + sock,
		"SWAYSOCK=" + filepath.Join(dir, "unavailable-sway"),
		"XDG_RUNTIME_DIR=" + dir,
	})
	manager, err := OpenRuntimeContext(ctx, rt)
	if err != nil {
		t.Fatalf("open Wayland fallback: %v", err)
	}
	if _, ok := manager.(*WaylandWindowManager); !ok {
		_ = manager.Close()
		t.Fatalf("runtime selected %T, want WaylandWindowManager", manager)
	}
	windows, err := manager.List(ctx)
	if err != nil || len(windows) != 1 || windows[0].ID != 77 || windows[0].Title != "Editor" {
		_ = manager.Close()
		t.Fatalf("Wayland fallback windows=%+v err=%v", windows, err)
	}
	if err := manager.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Wayland compositor: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Wayland connections did not close")
	}
}
