package input

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

func virtualGlobalPayload(name uint32, iface string, version uint32) []byte {
	n := len(iface) + 1
	pad := (n + 3) &^ 3
	buf := make([]byte, 8+pad+4)
	wl.PutUint32(buf, name)
	wl.PutUint32(buf[4:], uint32(n))
	copy(buf[8:], iface)
	wl.PutUint32(buf[8+pad:], version)
	return buf
}
func virtualEvent(conn *net.UnixConn, sender, opcode uint32, body []byte) error {
	if len(body)%4 != 0 {
		return fmt.Errorf("invalid synthetic event alignment: %d", len(body))
	}
	pkt := make([]byte, 8+len(body))
	wl.PutUint32(pkt[:4], sender)
	wl.PutUint32(pkt[4:8], uint32(len(pkt))<<16|opcode)
	copy(pkt[8:], body)
	_, err := conn.Write(pkt)
	return err
}
func virtualWords(words ...uint32) []byte {
	buf := make([]byte, 4*len(words))
	for i, w := range words {
		wl.PutUint32(buf[4*i:], w)
	}
	return buf
}

func virtualManagerCompositor(conn *net.UnixConn, advertised map[string]uint32, wantReady bool) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var registry, output, ptrMgr, kbdMgr uint32
	synced := 0
	var pointer, keyboard uint32
	for {
		var h [8]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("Wayland fake read: %w", err)
		}
		size := int(wl.Uint32(h[4:8]) >> 16)
		if size < 8 || size > 100000 {
			return fmt.Errorf("bad wire size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender, opcode := wl.Uint32(h[:4]), wl.Uint32(h[4:8])&0xffff
		if sender == 1 && opcode == 1 {
			registry = wl.Uint32(body[:4])
			continue
		}
		if sender == 1 && opcode == 0 {
			cb := wl.Uint32(body[:4])
			synced++
			if synced == 1 {
				for name, id := range map[string]uint32{"zwlr_virtual_pointer_manager_v1": 11, "zwp_virtual_keyboard_manager_v1": 12, "wl_seat": 13, "wl_output": 14} {
					if version := advertised[name]; version != 0 {
						if err := virtualEvent(conn, registry, 0, virtualGlobalPayload(id, name, version)); err != nil {
							return err
						}
					}
				}
			} else if output != 0 {
				if err := virtualEvent(conn, output, 1, virtualWords(0, 800, 600, 60000)); err != nil {
					return err
				}
				if err := virtualEvent(conn, output, 3, virtualWords(2)); err != nil {
					return err
				}
			}
			if err := virtualEvent(conn, cb, 0, virtualWords(0)); err != nil {
				return err
			}
			continue
		}
		if sender == registry && opcode == 0 && len(body) >= 8 {
			name := wl.Uint32(body[:4])
			id := wl.Uint32(body[len(body)-4:])
			switch name {
			case 11:
				ptrMgr = id
			case 12:
				kbdMgr = id
			}
			if name == 14 {
				output = id
				ver := wl.Uint32(body[len(body)-8 : len(body)-4])
				if ver > advertised["wl_output"] {
					return fmt.Errorf("invalid wl_output bind version %d", ver)
				}
			}
			continue
		}
		// A real manager must create both pointer and keyboard objects, then send
		// at least one keyboard warmup event before reporting the backend ready.
		if size >= 16 && opcode == 0 {
			if sender == ptrMgr {
				pointer = wl.Uint32(body[len(body)-4:])
			}
			if sender == kbdMgr {
				keyboard = wl.Uint32(body[len(body)-4:])
			}
		}
		if wantReady && pointer != 0 && keyboard != 0 && sender == keyboard && opcode == 1 {
			// The constructor warms up with both key down and key up. Keep
			// reading until Close so no successful write loses the transport.
			wantReady = false
		}
	}
}

func TestWlVirtualConstructorRejectsMissingAdvertisedProtocols(t *testing.T) {
	full := map[string]uint32{"zwlr_virtual_pointer_manager_v1": 2, "zwp_virtual_keyboard_manager_v1": 1, "wl_seat": 7, "wl_output": 2}
	for _, missing := range []string{"zwlr_virtual_pointer_manager_v1", "zwp_virtual_keyboard_manager_v1", "wl_seat"} {
		t.Run(missing, func(t *testing.T) {
			globals := make(map[string]uint32, len(full))
			for k, v := range full {
				if k != missing {
					globals[k] = v
				}
			}
			sock := filepath.Join(t.TempDir(), "wl.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, lintShadow151 := listener.AcceptUnix()
				if lintShadow151 != nil {
					serverDone <- lintShadow151
					return
				}
				serverDone <- virtualManagerCompositor(conn, globals, false)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			b, err := NewWlVirtualBackendContext(ctx, sock)
			if b != nil || err == nil || !strings.Contains(err.Error(), "advertised") {
				t.Fatalf("missing protocol %q was accepted: %v %v", missing, b, err)
			}
			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatalf("synthetic compositor: %v", serverErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("backend did not close rejected connection")
			}
		})
	}
}

func TestWlVirtualConstructorUsesRealAdvertisedGlobalsAndInputWire(t *testing.T) {
	globals := map[string]uint32{"zwlr_virtual_pointer_manager_v1": 2, "zwp_virtual_keyboard_manager_v1": 1, "wl_seat": 7, "wl_output": 2}
	socket := filepath.Join(t.TempDir(), "wl.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, lintShadow186 := listener.AcceptUnix()
		if lintShadow186 != nil {
			serverDone <- lintShadow186
			return
		}
		serverDone <- virtualManagerCompositor(conn, globals, true)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := NewWlVirtualBackendContext(ctx, socket)
	if err != nil {
		t.Fatalf("valid synthetic Wayland compositor rejected: %v", err)
	}
	defer b.Close()
	width, height := b.outputExtent()
	if width != 400 || height != 300 {
		t.Fatalf("physical 800x600 at 2x scale must give 400x300, got %dx%d", width, height)
	}
	if err := b.MouseMove(ctx, 140, 120); err != nil {
		t.Fatalf("virtual pointer failed after initialization: %v", err)
	}
	if err := b.Type(ctx, "Ab"); err != nil {
		t.Fatalf("virtual keyboard failed after initialization: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("compositor reported bad wire sequence: %v", serverErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("compositor did not observe backend cleanup")
	}
}
