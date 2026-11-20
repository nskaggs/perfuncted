package output

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func newWaylandOutputCleanupFixture(t *testing.T) (*net.UnixConn, *WaylandLister) {
	t.Helper()
	socket := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan *net.UnixConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	connection, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	var server *net.UnixConn
	select {
	case server = <-accepted:
		t.Cleanup(func() { _ = server.Close() })
	case err := <-acceptErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("Wayland connection was not accepted")
	}

	registry := &wl.Registry{}
	connection.Register(registry)
	lister := &WaylandLister{
		session:    &wl.Session{Ctx: connection, Registry: registry},
		xdgManager: &wl.RawProxy{},
	}
	return server, lister
}

func TestWaylandFailedXDGEnrichmentCleansUpAfterCancellation(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		disconnectCompositor bool
	}{
		{name: "live compositor receives release"},
		{name: "disconnected compositor reports cleanup failure", disconnectCompositor: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, lister := newWaylandOutputCleanupFixture(t)
			event := wl.GlobalEvent{Name: 17, Interface: "wl_output", Version: 4}
			output := newWaylandOutput(event)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bindErr := errors.New("synthetic XDG output enrichment failure")
			errs := lister.bindAddedOutputsWith(ctx, []*waylandOutput{output}, map[uint32]wl.GlobalEvent{event.Name: event}, func(context.Context, *waylandOutput) error {
				if tc.disconnectCompositor {
					if err := server.Close(); err != nil {
						t.Errorf("disconnect synthetic compositor: %v", err)
					}
				}
				cancel()
				return bindErr
			})
			if len(lister.outputs) != 0 {
				t.Fatalf("failed output enrichment published %d outputs", len(lister.outputs))
			}
			joined := errors.Join(errs...)
			if !errors.Is(joined, bindErr) {
				t.Fatalf("failed XDG enrichment errors = %v, want %v", errs, bindErr)
			}
			if tc.disconnectCompositor {
				if !errors.Is(joined, syscall.EPIPE) {
					t.Fatalf("cleanup errors = %v, want compositor write failure", errs)
				}
				return
			}
			if len(errs) != 1 {
				t.Fatalf("cleanup errors = %v, want only the XDG enrichment failure", errs)
			}

			if err := server.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			var header [8]byte
			if _, err := io.ReadFull(server, header[:]); err != nil {
				t.Fatalf("read output bind request: %v", err)
			}
			requestSize := int(wl.Uint32(header[4:8]) >> 16)
			if requestSize < len(header) {
				t.Fatalf("invalid output bind request size %d", requestSize)
			}
			if _, err := io.CopyN(io.Discard, server, int64(requestSize-len(header))); err != nil {
				t.Fatalf("read output bind request body: %v", err)
			}

			if _, err := io.ReadFull(server, header[:]); err != nil {
				t.Fatalf("read wl_output release after canceled XDG enrichment: %v", err)
			}
			if gotSender, gotOpcode := wl.Uint32(header[:4]), wl.Uint32(header[4:8])&0xffff; gotSender != output.proxy.ID() || gotOpcode != 0 {
				t.Fatalf("cleanup request sender/opcode = %d/%d, want output proxy %d release", gotSender, gotOpcode, output.proxy.ID())
			}
		})
	}
}
