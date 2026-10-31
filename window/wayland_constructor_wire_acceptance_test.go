package window

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func wireWindowGlobal(id uint32, iface string, version uint32) []byte {
	n := len(iface) + 1
	pad := (n + 3) &^ 3
	body := make([]byte, 8+pad+4)
	wl.PutUint32(body[0:], id)
	wl.PutUint32(body[4:], uint32(n))
	copy(body[8:], iface)
	wl.PutUint32(body[8+pad:], version)
	return body
}
func wireWindowEvent(conn *net.UnixConn, sender, opcode uint32, body []byte) error {
	msg := make([]byte, 8+len(body))
	wl.PutUint32(msg[0:], sender)
	wl.PutUint32(msg[4:], uint32(len(msg))<<16|opcode)
	copy(msg[8:], body)
	_, err := conn.Write(msg)
	return err
}
func wireWindowWords(words ...uint32) []byte {
	b := make([]byte, 4*len(words))
	for i, v := range words {
		wl.PutUint32(b[4*i:], v)
	}
	return b
}
func wireWindowString(v string) []byte {
	n := len(v) + 1
	padded := (n + 3) &^ 3
	buf := make([]byte, 4+padded)
	wl.PutUint32(buf, uint32(n))
	copy(buf[4:], v)
	return buf
}
func serveWindowCompositor(conn *net.UnixConn, protocol string) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var registryID, managerID, seatID uint32
	syncs := 0
	for {
		var h [8]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		size := int(wl.Uint32(h[4:8]) >> 16)
		if size < 8 || size > 65535 {
			return fmt.Errorf("invalid request size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender, opcode := wl.Uint32(h[:4]), wl.Uint32(h[4:8])&0xffff
		if sender == 1 && opcode == 1 {
			registryID = wl.Uint32(body)
			continue
		}
		if sender == registryID && opcode == 0 {
			name := wl.Uint32(body)
			newID := wl.Uint32(body[len(body)-4:])
			ver := wl.Uint32(body[len(body)-8 : len(body)-4])
			switch name {
			case 41:
				if protocol != "wlr" && protocol != "both" {
					return fmt.Errorf("wlr manager unexpectedly bound")
				}
				if ver != 3 {
					return fmt.Errorf("wrong wlr manager version %d", ver)
				}
				managerID = newID
			case 42:
				if protocol != "ext" {
					return fmt.Errorf("unexpected ext manager bound")
				}
				if ver != 1 {
					return fmt.Errorf("wrong ext manager version %d", ver)
				}
				managerID = newID
			case 43:
				if ver != 1 {
					return fmt.Errorf("wrong seat bind version %d", ver)
				}
				seatID = newID
			default:
				return fmt.Errorf("unexpected global %d bound", name)
			}
			continue
		}
		if sender == 1 && opcode == 0 {
			cb := wl.Uint32(body)
			syncs++
			if syncs == 1 {
				if protocol == "wlr" || protocol == "both" {
					if err := wireWindowEvent(conn, registryID, 0, wireWindowGlobal(41, "zwlr_foreign_toplevel_manager_v1", 3)); err != nil {
						return err
					}
				}
				if protocol == "ext" || protocol == "both" {
					if err := wireWindowEvent(conn, registryID, 0, wireWindowGlobal(42, "ext_foreign_toplevel_list_v1", 1)); err != nil {
						return err
					}
				}
				if protocol != "none" {
					if err := wireWindowEvent(conn, registryID, 0, wireWindowGlobal(43, "wl_seat", 9)); err != nil {
						return err
					}
				}
			}
			if syncs == 2 && managerID != 0 {
				if seatID == 0 {
					return errors.New("Wayland seat was not bound before manager sync")
				}
				if err := wireWindowEvent(conn, managerID, 0, wireWindowWords(77)); err != nil {
					return err
				}
				titleOp := uint32(0)
				if protocol == "ext" {
					titleOp = 2
				}
				if err := wireWindowEvent(conn, 77, titleOp, wireWindowString("Editor")); err != nil {
					return err
				}
				if protocol == "ext" {
					if err := wireWindowEvent(conn, 77, 4, wireWindowString("stable-editor-7")); err != nil {
						return err
					}
				}
			}
			if err := wireWindowEvent(conn, cb, 0, wireWindowWords(0)); err != nil {
				return err
			}
			continue
		}
	}
}

func TestWaylandWindowManagerRealSyntheticConstructorAndToplevelDiscovery(t *testing.T) {
	for _, proto := range []string{"wlr", "ext", "both", "none"} {
		t.Run(proto, func(t *testing.T) {
			sock := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, lintShadow166 := listener.AcceptUnix()
				if lintShadow166 != nil {
					done <- lintShadow166
					return
				}
				done <- serveWindowCompositor(conn, proto)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			wm, err := NewWaylandWindowManagerForSocketContext(ctx, sock)
			if proto == "none" {
				if wm != nil || err == nil || !strings.Contains(err.Error(), "neither") {
					t.Fatalf("unsupported compositor incorrectly initialized: %v %v", wm, err)
				}
			} else {
				if err != nil {
					t.Fatalf("valid compositor refused: %v", err)
				}
				if wm == nil || wm.seat == nil {
					t.Fatal("constructor did not bind advertised seat")
				}
				windows, listErr := wm.List(ctx)
				if listErr != nil || len(windows) != 1 || windows[0].Title != "Editor" || windows[0].ID != 77 {
					t.Fatalf("live toplevels=%+v error=%v", windows, listErr)
				}
				if proto == "ext" {
					if windows[0].NativeID != "stable-editor-7" {
						t.Fatalf("stable protocol ID lost: %+v", windows[0])
					}
				}
				if proto == "both" && wm.wlrMgrID == 0 {
					t.Fatalf("failed to prefer control-capable manager")
				}
				if err := wm.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case serverErr := <-done:
				if serverErr != nil {
					t.Fatalf("synthetic Wayland server: %v", serverErr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("compositor connection not released")
			}
		})
	}
}

func TestWaylandWindowEnumerationStableUnderEveryMetadataTie(t *testing.T) {
	base := Info{ID: 77, NativeID: "shared", Title: "Editor", AppID: "com.example.Editor", Class: "edit", PID: 450, X: 10, Y: 20, W: 640, H: 480}
	for _, tc := range []struct {
		name   string
		change func(*Info)
	}{
		{"native handle", func(i *Info) { i.ID++ }},
		{"title", func(i *Info) { i.Title = "Terminal" }},
		{"application", func(i *Info) { i.AppID = "com.example.Terminal" }},
		{"class", func(i *Info) { i.Class = "terminal" }},
		{"process id", func(i *Info) { i.PID++ }},
		{"x coordinate", func(i *Info) { i.X++ }},
		{"y coordinate", func(i *Info) { i.Y++ }},
		{"width", func(i *Info) { i.W++ }},
		{"height", func(i *Info) { i.H++ }},
		{"active", func(i *Info) { i.Active = true }},
		{"minimized", func(i *Info) { i.Minimized = true }},
		{"maximized", func(i *Info) { i.Maximized = true }},
		{"fullscreen", func(i *Info) { i.Fullscreen = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			second := base
			tc.change(&second)
			wm, _, _ := newStubWaylandManager("", true, false)
			wm.toplevels = map[uint32]*Info{9: &second, 3: &base}
			for range 12 {
				windows, err := wm.List(context.Background())
				if err != nil || len(windows) != 2 {
					t.Fatalf("window enumeration failed: %+v %v", windows, err)
				}
				if compareWaylandWindows(windows[0], windows[1]) >= 0 || windows[0] != base || windows[1] != second {
					t.Fatalf("unstable ordering when %s differs: %+v", tc.name, windows)
				}
			}
		})
	}
}
