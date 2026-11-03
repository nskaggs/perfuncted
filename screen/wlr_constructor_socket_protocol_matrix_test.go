package screen

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Minimal compositor validates the real socket connection, registry enumeration,
// and sync callback used by the public wlr screenshot constructor.
func TestWlrConstructorRealWaylandSocketProtocolDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, advertised string
		wantSuccess      bool
	}{
		{"screencopy advertised", "zwlr_screencopy_manager_v1", true},
		{"no screencopy advertised", "wl_seat", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "wl.sock"))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan error, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
				var registry uint32
				for {
					var header [8]byte
					if _, err := io.ReadFull(conn, header[:]); err != nil {
						if errors.Is(err, io.EOF) {
							done <- nil
						} else {
							done <- err
						}
						return
					}
					wire := wl.Uint32(header[4:])
					size := int(wire >> 16)
					if size < 8 || size > 65535 {
						done <- errors.New("invalid request size")
						return
					}
					payload := make([]byte, size-8)
					if _, err := io.ReadFull(conn, payload); err != nil {
						done <- err
						return
					}
					if wl.Uint32(header[:4]) != 1 || len(payload) != 4 {
						done <- errors.New("unexpected request")
						return
					}
					switch wire & 0xffff {
					case 1:
						registry = wl.Uint32(payload)
					case 0:
						callback := wl.Uint32(payload)
						iface := tc.advertised
						length := len(iface) + 1
						padded := (length + 3) &^ 3
						event := make([]byte, 8+padded+4)
						wl.PutUint32(event[0:4], 52)
						wl.PutUint32(event[4:8], uint32(length))
						copy(event[8:], iface)
						wl.PutUint32(event[8+padded:], 1)
						if err := sendWlrTestEvent(conn, registry, 0, event); err != nil {
							done <- err
							return
						}
						if err := sendWlrTestEvent(conn, callback, 0, make([]byte, 4)); err != nil {
							done <- err
							return
						}
					default:
						done <- errors.New("unexpected request opcode")
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			b, err := NewWlrScreencopyBackendForSocketContext(ctx, listener.Addr().String())
			if tc.wantSuccess {
				if err != nil || b == nil {
					t.Fatalf("advertised protocol not accepted: backend=%v err=%v", b, err)
				}
				if closeErr := b.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			} else if b != nil || err == nil || !strings.Contains(err.Error(), "does not advertise") {
				t.Fatalf("unadvertised protocol accepted: backend=%v err=%v", b, err)
			}
			select {
			case serverErr := <-done:
				if serverErr != nil {
					t.Fatal(serverErr)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("constructor leaked Wayland session socket")
			}
		})
	}
}

func sendWlrTestEvent(conn net.Conn, id, opcode uint32, body []byte) error {
	msg := make([]byte, 8+len(body))
	wl.PutUint32(msg[:4], id)
	wl.PutUint32(msg[4:8], uint32(len(msg))<<16|opcode)
	copy(msg[8:], body)
	_, err := conn.Write(msg)
	return err
}
