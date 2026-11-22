package output

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWaylandListerRejectsDisconnectDuringInitialOutputSync(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		var registry uint32
		var syncs, outputBinds int
		for {
			message, readErr := readWaylandMessage(conn)
			if readErr != nil {
				serverDone <- readErr
				return
			}
			sender := wl.Uint32(message[:4])
			opcode := wl.Uint32(message[4:8]) & 0xffff
			body := message[8:]
			switch {
			case sender == 1 && opcode == 1:
				registry = wl.Uint32(body)
			case sender == registry && opcode == 0:
				global, _, bindErr := parseWaylandBind(body)
				if bindErr != nil || global != 4 {
					serverDone <- errors.New("unexpected output bind during initialization")
					return
				}
				outputBinds++
			case sender == 1 && opcode == 0:
				syncs++
				if len(body) < 4 {
					serverDone <- errors.New("display sync request omitted callback ID")
					return
				}
				callback := wl.Uint32(body)
				if syncs == 1 {
					global := appendUint32(nil, 4)
					global = appendWlStringData(global, "wl_output")
					global = appendUint32(global, 4)
					if writeErr := writeWaylandMessage(conn, registry, 0, global); writeErr != nil {
						serverDone <- writeErr
						return
					}
					if writeErr := writeWaylandMessage(conn, callback, 0, make([]byte, 4)); writeErr != nil {
						serverDone <- writeErr
						return
					}
					continue
				}
				if outputBinds != 1 {
					serverDone <- errors.New("output synchronization started before the output bind")
					return
				}
				serverDone <- nil
				return
			default:
				serverDone <- errors.New("unexpected Wayland request during initialization")
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lister, err := NewWaylandListerContext(ctx, socket)
	if lister != nil {
		_ = lister.Close()
		t.Fatal("lister published after the compositor disconnected during initial sync")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("lister initialization error = %v, want compositor disconnect", err)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic compositor did not observe initial output sync: %v", ctx.Err())
	}
}
