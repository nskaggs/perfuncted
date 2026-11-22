//go:build linux

package window

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRuntimeUsesToplevelBackendForUnknownExtWayland(t *testing.T) {
	socket := shortPrivateSocketPath(t, "wayland.sock")
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
