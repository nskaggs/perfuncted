package screen

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

func extConstructorGlobal(name uint32, iface string, version uint32) []byte {
	n := len(iface) + 1
	pad := (n + 3) &^ 3
	data := make([]byte, 8+pad+4)
	wl.PutUint32(data, name)
	wl.PutUint32(data[4:], uint32(n))
	copy(data[8:], iface)
	wl.PutUint32(data[8+pad:], version)
	return data
}
func serveExtConstructorPeer(conn *net.UnixConn, skip string) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	var registryID uint32
	gotBinds := map[uint32]uint32{}
	syncs := 0
	globals := []struct {
		name    uint32
		iface   string
		version uint32
	}{
		{21, "ext_image_copy_capture_manager_v1", 1}, {22, "ext_output_image_capture_source_manager_v1", 1},
		{23, "wl_output", 4}, {24, "wl_shm", 1},
	}
	for {
		var h [8]byte
		if _, err := io.ReadFull(conn, h[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("peer read: %w", err)
		}
		size := int(wl.Uint32(h[4:]) >> 16)
		if size < 8 || size > 16384 {
			return fmt.Errorf("invalid request length %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender, opcode := wl.Uint32(h[:4]), wl.Uint32(h[4:])&0xffff
		if sender == 1 && opcode == 1 {
			registryID = wl.Uint32(body)
			continue
		}
		if sender == 1 && opcode == 0 {
			callback := wl.Uint32(body)
			syncs++
			if syncs == 1 {
				for _, global := range globals {
					if global.iface == skip {
						continue
					}
					if err := writeExtTestEvent(conn, registryID, 0, extConstructorGlobal(global.name, global.iface, global.version)); err != nil {
						return err
					}
				}
			}
			if err := writeExtTestEvent(conn, callback, 0, wlUint32Payload(0)); err != nil {
				return err
			}
			continue
		}
		if sender == registryID && opcode == 0 {
			if size < 20 {
				return fmt.Errorf("short registry.bind %x", body)
			}
			name, version, newID := wl.Uint32(body), wl.Uint32(body[len(body)-8:]), wl.Uint32(body[len(body)-4:])
			if (name == 23 && version > 2) || (name == 21 && version > 1) || (name == 22 && version > 1) {
				return fmt.Errorf("invalid bound version %d for global %d", version, name)
			}
			gotBinds[name] = newID
			continue
		}
		if gotBinds[22] != 0 && sender == gotBinds[22] && opcode == 0 {
			if len(body) < 8 {
				return fmt.Errorf("invalid source creation %x", body)
			}
			continue
		}
		if gotBinds[21] != 0 && sender == gotBinds[21] && opcode == 0 {
			if len(body) < 12 {
				return fmt.Errorf("invalid copy session creation %x", body)
			}
			continue
		}
	}
}
func TestExtCaptureRealSyntheticConstructorBindsOnlyAdvertisedProtocols(t *testing.T) {
	cases := []struct{ name, skip, wantErr string }{
		{"full capture protocol stack", "", ""},
		{"copy manager absent", "ext_image_copy_capture_manager_v1", "ext_image_copy_capture_manager_v1"},
		{"output source manager absent", "ext_output_image_capture_source_manager_v1", "ext_output_image_capture_source_manager_v1"},
		{"output absent", "wl_output", "wl_output"},
		{"shared memory absent", "wl_shm", "wl_shm"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			server := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					server <- err
					return
				}
				server <- serveExtConstructorPeer(conn, tc.skip)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			b, err := NewExtCaptureBackendForSocketContext(ctx, socket)
			if tc.wantErr != "" {
				if b != nil || err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("missing %s accepted: backend=%v err=%v", tc.skip, b, err)
				}
			} else {
				if err != nil {
					t.Fatalf("fully capable compositor failed: %v", err)
				}
				if b.session == nil || b.shm == nil || b.outputProxy == nil || b.mgrProxy == nil || b.sourceMgrProxy == nil || b.sourceProxy == nil || b.sessProxy == nil {
					t.Fatal("successful constructor returned incomplete capture backend")
				}
				if !b.CanonicalHashing() {
					t.Fatal("external capture did not advertise canonical hashes")
				}
				if err := b.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case result := <-server:
				if result != nil {
					t.Fatalf("synthetic compositor rejected wire requests: %v", result)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("backend failed to release compositor connection")
			}
		})
	}
}
