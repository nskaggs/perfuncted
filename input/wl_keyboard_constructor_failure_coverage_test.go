//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func newKeyboardConstructorTransport(t *testing.T) (*wl.Context, *wl.Registry, *net.UnixConn) {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	peerReady := make(chan *net.UnixConn, 1)
	peerErr := make(chan error, 1)
	go func() {
		peer, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			peerErr <- acceptErr
			return
		}
		peerReady <- peer
	}()

	ctx, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	var peer *net.UnixConn
	select {
	case peer = <-peerReady:
	case err := <-peerErr:
		t.Fatalf("accept Wayland client: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	registry := &wl.Registry{}
	ctx.Register(registry)
	return ctx, registry, peer
}

func readKeyboardConstructorRequest(t *testing.T, peer *net.UnixConn) (uint32, uint32, []byte) {
	t.Helper()
	var header [8]byte
	if _, err := io.ReadFull(peer, header[:]); err != nil {
		t.Fatalf("read Wayland request header: %v", err)
	}
	word := binary.LittleEndian.Uint32(header[4:])
	size := int(word >> 16)
	if size < len(header) || size%4 != 0 {
		t.Fatalf("invalid Wayland request size %d", size)
	}
	body := make([]byte, size-len(header))
	if _, err := io.ReadFull(peer, body); err != nil {
		t.Fatalf("read Wayland request body: %v", err)
	}
	return binary.LittleEndian.Uint32(header[:4]), word & 0xffff, body
}

func TestNewWlKeyboardReturnsCanceledSeatBind(t *testing.T) {
	ctx, registry, _ := newKeyboardConstructorTransport(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	kbd, err := newWlKeyboard(canceled, ctx, registry, 12, 1, 13)
	if kbd != nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "bind wl_seat") {
		t.Fatalf("canceled seat bind returned keyboard=%v err=%v", kbd, err)
	}
}

func TestNewWlKeyboardRejectsZeroManagerVersionAfterSeatBind(t *testing.T) {
	ctx, registry, peer := newKeyboardConstructorTransport(t)
	kbd, err := newWlKeyboard(context.Background(), ctx, registry, 12, 0, 13)
	if kbd != nil || err == nil || !strings.Contains(err.Error(), "bind virtual keyboard manager") || !strings.Contains(err.Error(), "version=0") {
		t.Fatalf("zero manager version returned keyboard=%v err=%v", kbd, err)
	}

	sender, opcode, body := readKeyboardConstructorRequest(t, peer)
	if sender != registry.ID() || opcode != 0 || len(body) != 24 {
		t.Fatalf("first constructor request sender=%d opcode=%d body=%d bytes", sender, opcode, len(body))
	}
	if name := binary.LittleEndian.Uint32(body[:4]); name != 13 {
		t.Fatalf("seat bind targeted global %d, want 13", name)
	}
	if length := binary.LittleEndian.Uint32(body[4:8]); length != 8 || string(body[8:16]) != "wl_seat\x00" {
		t.Fatalf("seat bind interface length=%d name=%q, want wl_seat", length, body[8:16])
	}
	if version := binary.LittleEndian.Uint32(body[16:20]); version != 1 {
		t.Fatalf("seat bind version = %d, want 1", version)
	}
	if id := binary.LittleEndian.Uint32(body[20:24]); id == 0 {
		t.Fatal("seat bind used a zero client object id")
	}
}
