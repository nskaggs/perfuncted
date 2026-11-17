package output

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

func TestWaylandListReportsFailedSyncAfterBindingNewOutput(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "wayland.sock")
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
		var registryID uint32
		var roundTrips, outputBinds int
		for {
			message, readErr := readWaylandMessage(conn)
			if errors.Is(readErr, io.EOF) {
				serverDone <- nil
				return
			}
			if readErr != nil {
				serverDone <- fmt.Errorf("read Wayland request: %w", readErr)
				return
			}
			sender := wl.Uint32(message[:4])
			opcode := wl.Uint32(message[4:8]) & 0xffff
			body := message[8:]
			switch {
			case sender == 1 && opcode == 1:
				registryID = wl.Uint32(body)
			case sender == registryID && opcode == 0:
				globalName, _, bindErr := parseWaylandBind(body)
				if bindErr != nil {
					serverDone <- bindErr
					return
				}
				if globalName != 4 && globalName != 5 {
					serverDone <- fmt.Errorf("unexpected output global bind %d", globalName)
					return
				}
				outputBinds++
			case sender == 1 && opcode == 0:
				roundTrips++
				if len(body) < 4 {
					serverDone <- errors.New("display sync request omitted callback ID")
					return
				}
				callback := wl.Uint32(body[:4])
				switch roundTrips {
				case 1, 3:
					globalID := uint32(4)
					if roundTrips == 3 {
						globalID = 5
					}
					global := appendUint32(nil, globalID)
					global = appendWlStringData(global, "wl_output")
					global = appendUint32(global, 4)
					if writeErr := writeWaylandMessage(conn, registryID, 0, global); writeErr != nil {
						serverDone <- fmt.Errorf("announce output global: %w", writeErr)
						return
					}
				case 4:
					if outputBinds != 2 {
						serverDone <- fmt.Errorf("output binds = %d, want initial and new output", outputBinds)
						return
					}
					if closeErr := conn.Close(); closeErr != nil {
						serverDone <- closeErr
						return
					}
					serverDone <- nil
					return
				}
				if writeErr := writeWaylandMessage(conn, callback, 0, make([]byte, 4)); writeErr != nil {
					serverDone <- fmt.Errorf("complete display sync: %w", writeErr)
					return
				}
			default:
				serverDone <- fmt.Errorf("unexpected Wayland request sender=%d opcode=%d", sender, opcode)
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lister, err := NewWaylandListerContext(ctx, socket)
	if err != nil {
		t.Fatalf("initialize initial output: %v", err)
	}
	defer func() { _ = lister.Close() }()

	outputs, err := lister.List(ctx)
	if err == nil || !strings.Contains(err.Error(), "synchronize new output") {
		t.Fatalf("List returned outputs=%+v err=%v, want synchronized-add failure", outputs, err)
	}
	if outputs != nil {
		t.Fatalf("failed topology synchronization published partial outputs: %+v", outputs)
	}
	select {
	case serverErr := <-serverDone:
		if serverErr != nil {
			t.Fatalf("synthetic compositor: %v", serverErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic compositor did not reach the failing sync: %v", ctx.Err())
	}
}
