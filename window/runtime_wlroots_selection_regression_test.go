//go:build linux

package window

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestWindowRuntimePrefersSwayIPCOnWlroots(t *testing.T) {
	dir := shortPrivateSocketDir(t)
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
	dir := shortPrivateSocketDir(t)
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
