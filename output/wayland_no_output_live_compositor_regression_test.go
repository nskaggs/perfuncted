package output

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

// An otherwise responsive compositor may not advertise any usable output.
// Discovery must not mistake a successful registry sync for an initialized
// display lister or retain its socket after refusing the capability.
type unusableWaylandGlobal struct {
	id      uint32
	iface   string
	version uint32
}

func TestWaylandListerRejectsResponsiveCompositorWithoutUsableOutputs(t *testing.T) {
	for _, tc := range []struct {
		name    string
		globals []unusableWaylandGlobal
	}{
		{name: "empty registry"},
		{name: "unrelated seat only", globals: []unusableWaylandGlobal{{7, "wl_seat", 7}}},
		{name: "zero output version", globals: []unusableWaylandGlobal{{9, "wl_output", 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() { serverDone <- serveUnusableWaylandCompositor(listener, tc.globals) }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			lister, openErr := NewWaylandListerContext(ctx, socket)
			if lister != nil || openErr == nil || !strings.Contains(openErr.Error(), "no wl_output globals advertised") {
				if lister != nil {
					_ = lister.Close()
				}
				t.Fatalf("unusable compositor produced lister=%v err=%v", lister, openErr)
			}

			select {
			case serveErr := <-serverDone:
				if serveErr != nil {
					t.Fatalf("compositor did not observe clean client disconnect: %v", serveErr)
				}
			case <-time.After(time.Second):
				t.Fatal("failed discovery leaked compositor connection")
			}
		})
	}
}

func serveUnusableWaylandCompositor(listener net.Listener, globals []unusableWaylandGlobal) error {
	conn, acceptErr := listener.Accept()
	if acceptErr != nil {
		return acceptErr
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	var registry uint32
	for {
		msg, readErr := readWaylandMessage(conn)
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		sender, opcode := wl.Uint32(msg[:4]), wl.Uint32(msg[4:8])&0xffff
		body := msg[8:]
		switch {
		case sender == 1 && opcode == 1:
			registry = wl.Uint32(body)
		case sender == 1 && opcode == 0:
			for _, ev := range globals {
				payload := appendUint32(nil, ev.id)
				payload = appendWlStringData(payload, ev.iface)
				payload = appendUint32(payload, ev.version)
				if writeErr := writeWaylandMessage(conn, registry, 0, payload); writeErr != nil {
					return writeErr
				}
			}
			if writeErr := writeWaylandMessage(conn, wl.Uint32(body), 0, make([]byte, 4)); writeErr != nil {
				return writeErr
			}
		default:
			return errors.New("unexpected client request to compositor without output")
		}
	}
}
