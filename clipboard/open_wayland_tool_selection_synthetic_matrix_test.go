package clipboard

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// The socket is a private Unix listener, never a desktop session. The tools
// are inert stubs: backend selection must not execute either clipboard tool.
func TestClipboardOpenRuntimeSelectsWaylandOnlyWhenSocketAndBothToolsExist(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "wayland-test")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// A real private Wayland wire handshake lets compositor detection finish;
	// the test intentionally never contacts the host desktop.
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				for {
					var header [8]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						return
					}
					encoded := wl.Uint32(header[4:])
					size := int(encoded >> 16)
					if size < 8 || size > 4096 {
						return
					}
					payload := make([]byte, size-8)
					if _, err := io.ReadFull(conn, payload); err != nil {
						return
					}
					if wl.Uint32(header[0:4]) != 1 {
						return
					}
					switch encoded & 0xffff {
					case 1: // get_registry, no globals advertised
					case 0: // sync callback
						if len(payload) < 4 {
							return
						}
						callback := wl.Uint32(payload)
						var reply [12]byte
						wl.PutUint32(reply[:4], callback)
						wl.PutUint32(reply[4:8], 12<<16)
						if _, err := conn.Write(reply[:]); err != nil {
							return
						}
					default:
						return
					}
				}
			}()
		}
	}()
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, tool := range []string{"wl-copy", "wl-paste", "xclip"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nexit 77\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-test", "XDG_RUNTIME_DIR=" + dir, "XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=sway", "DISPLAY=:unused-test", "CUSTOM_SELECTION_MARKER=selection-fixture"})
	clip, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	selected, ok := clip.(*extCmdClipboard)
	if !ok {
		t.Fatalf("unexpected Wayland backend %T", clip)
	}
	if !slices.Equal(selected.getCmd, []string{"wl-paste", "--no-newline"}) || !slices.Equal(selected.setCmd, []string{"wl-copy", "--foreground"}) {
		t.Fatalf("wrong Wayland subprocess contract: get=%v set=%v", selected.getCmd, selected.setCmd)
	}
	if !slices.Contains(selected.env, "CUSTOM_SELECTION_MARKER=selection-fixture") {
		t.Fatalf("lost runtime-scoped environment: %v", selected.env)
	}
	if err := clip.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bin, "wl-paste")); err != nil {
		t.Fatal(err)
	}
	fallback, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	selected, ok = fallback.(*extCmdClipboard)
	if !ok || len(selected.getCmd) == 0 || selected.getCmd[0] != "xclip" {
		t.Fatalf("missing Wayland paste tool selected unsafe backend: %T %+v", fallback, selected)
	}
	if err := fallback.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bin, "xclip")); err != nil {
		t.Fatal(err)
	}
	if candidate, err := OpenRuntimeContext(context.Background(), rt); candidate != nil || !errors.Is(err, ErrNoClipboardTool) {
		t.Fatalf("incomplete tools accepted: %T %v", candidate, err)
	}
}
