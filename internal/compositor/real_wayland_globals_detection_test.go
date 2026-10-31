//go:build linux

package compositor

import (
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/wl"
)

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
		{"unrecognized protocol", []string{"wl_output", "wl_seat"}, "KDE", Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
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
