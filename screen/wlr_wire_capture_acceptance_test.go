package screen

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/wl"
)

func wlrGlobalPayload(name uint32, iface string, version uint32) []byte {
	n := len(iface) + 1
	padded := (n + 3) &^ 3
	result := make([]byte, 8+padded+4)
	wl.PutUint32(result[:4], name)
	wl.PutUint32(result[4:8], uint32(n))
	copy(result[8:], iface)
	wl.PutUint32(result[8+padded:], version)
	return result
}

// wlrFakeCompositor exercises the real registry, binder, screencopy frame,
// shared-memory setup, and ready/error dispatch over a Unix socket.
func wlrFakeCompositor(conn *net.UnixConn, frameOutcome string) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(8 * time.Second))
	var registryID, managerID, outputID, frameID uint32
	var sawCapture, sawCopy bool
	for {
		var header [8]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			if errors.Is(err, io.EOF) && sawCapture {
				return nil
			}
			return fmt.Errorf("read Wayland request: %w", err)
		}
		size := int(wl.Uint32(header[4:8]) >> 16)
		if size < 8 || size > 8192 {
			return fmt.Errorf("invalid Wayland request size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return err
		}
		sender := wl.Uint32(header[:4])
		opcode := wl.Uint32(header[4:8]) & 0xffff
		if sender == 1 && opcode == 1 { // get registry
			registryID = wl.Uint32(body[:4])
			continue
		}
		if sender == 1 && opcode == 0 { // sync callback completes after advertised globals
			callback := wl.Uint32(body[:4])
			if managerID == 0 {
				for _, ev := range []struct {
					name    uint32
					iface   string
					version uint32
				}{
					{41, "zwlr_screencopy_manager_v1", 3}, {42, "wl_shm", 1}, {43, "wl_output", map[bool]uint32{true: 1, false: 4}[frameOutcome == "output v1"]},
				} {
					if err := writeExtTestEvent(conn, registryID, 0, wlrGlobalPayload(ev.name, ev.iface, ev.version)); err != nil {
						return err
					}
				}
			} else if outputID != 0 {
				if err := writeExtTestEvent(conn, outputID, 1, wlUint32Payload(0, 2, 2, 60000)); err != nil {
					return err
				}
				if err := writeExtTestEvent(conn, outputID, 3, wlUint32Payload(1)); err != nil {
					return err
				}
			}
			if err := writeExtTestEvent(conn, callback, 0, wlUint32Payload(0)); err != nil {
				return err
			}
			continue
		}
		if sender == registryID && opcode == 0 {
			name := wl.Uint32(body[:4])
			if name == 43 && frameOutcome == "output v1" {
				requestedVersion := wl.Uint32(body[len(body)-8 : len(body)-4])
				if requestedVersion != 1 {
					return fmt.Errorf("wl_output v1 compositor rejected requested bind version %d", requestedVersion)
				}
			}
			newID := wl.Uint32(body[len(body)-4:])
			switch name {
			case 41:
				managerID = newID
			case 43:
				outputID = newID
			}
			continue
		}
		if sender == managerID && opcode == 0 {
			frameID = wl.Uint32(body[:4])
			sawCapture = true
			if frameOutcome == "failed before buffer" {
				return writeExtTestEvent(conn, frameID, 3, nil)
			}
			if frameOutcome == "invalid buffer" {
				return writeExtTestEvent(conn, frameID, 0, []byte{0, 0, 0, 0})
			}
			if err := writeExtTestEvent(conn, frameID, 0, wlUint32Payload(0, 2, 2, 8)); err != nil {
				return err
			}
			continue
		}
		if frameID != 0 && sender == frameID && opcode == 0 { // copy
			sawCopy = true
			if frameOutcome == "failed after copy" {
				return writeExtTestEvent(conn, frameID, 3, nil)
			}
			if err := writeExtTestEvent(conn, frameID, 2, wlUint32Payload(0, 0, 0)); err != nil {
				return err
			}
			frameID = 0
			continue
		}
		_ = sawCopy
	}
}

func TestWlrScreencopySyntheticProtocolCaptureAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome string
		op      func(*WlrScreencopyBackend) (image.Image, uint32, error)
		wantErr bool
		rect    image.Rectangle
	}{
		{name: "output version one", outcome: "output v1", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}},
		{name: "full image", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}},
		{name: "repeated full image", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			if _, err := b.GrabFullHash(context.Background()); err != nil {
				return nil, 0, err
			}
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}},
		{name: "cropped image", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rect(1, 1, 2, 2))
			return img, 0, err
		}},
		{name: "full hash", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			h, err := b.GrabFullHash(context.Background())
			return nil, h, err
		}},
		{name: "region hash", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1))
			return nil, h, err
		}, rect: image.Rect(0, 0, 1, 1)},
		{name: "region outside framebuffer", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			h, err := b.GrabRegionHash(context.Background(), image.Rect(500, 500, 510, 510))
			return nil, h, err
		}, wantErr: true},
		{name: "failed before buffer", outcome: "failed before buffer", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}, wantErr: true},
		{name: "failed after copy", outcome: "failed after copy", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			h, err := b.GrabFullHash(context.Background())
			return nil, h, err
		}, wantErr: true},
		{name: "invalid buffer", outcome: "invalid buffer", op: func(b *WlrScreencopyBackend) (image.Image, uint32, error) {
			img, err := b.Grab(context.Background(), image.Rectangle{})
			return img, 0, err
		}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "wayland.sock")
			server, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			serverDone := make(chan error, 1)
			go func() {
				conn, e := server.AcceptUnix()
				if e != nil {
					serverDone <- e
					return
				}
				serverDone <- wlrFakeCompositor(conn, tc.outcome)
			}()
			b := newWlrScreencopyBackendWithContextConnector(socket, wl.ConnectContext, time.Minute)
			img, hash, captureErr := tc.op(b)
			if tc.wantErr {
				if captureErr == nil || img != nil {
					t.Fatalf("compositor failure accepted: image=%v err=%v", img, captureErr)
				}
				if tc.name == "region outside framebuffer" {
					if !errors.Is(captureErr, find.ErrEmptyRegion) {
						t.Fatalf("invalid region did not expose ErrEmptyRegion: %v", captureErr)
					}
				} else if !strings.Contains(captureErr.Error(), "failed") {
					t.Fatalf("failure did not surface compositor error: %v", captureErr)
				}
			} else {
				if captureErr != nil {
					t.Fatalf("wire capture failed: %v", captureErr)
				}
				if strings.Contains(tc.name, "image") || tc.name == "output version one" {
					want := image.Pt(2, 2)
					if tc.name == "cropped image" {
						want = image.Pt(1, 1)
					}
					if img == nil || img.Bounds().Size() != want {
						t.Fatalf("wrong image: %v want size %v", img, want)
					}
				} else {
					want, e := find.PixelHashBGRA(make([]byte, 16), 2, 2, 8, tc.rect)
					if e != nil {
						t.Fatal(e)
					}
					if hash != want {
						t.Fatalf("hash=%08x want=%08x", hash, want)
					}
				}
			}
			if err := b.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("synthetic Wayland server: %v", err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("compositor did not terminate")
			}
		})
	}
}
