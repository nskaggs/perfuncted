package input

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestEIClientReassemblesFragmentedRealUnixFramesInOrder(t *testing.T) {
	clientConn, serverConn := unixConnectionPair(t)
	defer serverConn.Close()
	client := newEIClient(clientConn)
	defer client.close()
	messages := []eiMessage{
		{object: eiServerIDFloor + 1, opcode: 5, body: eiU32(1)},
		{object: eiServerIDFloor + 2, opcode: 7, body: eiU32(2)},
		{object: eiServerIDFloor + 3, opcode: 8, body: eiU32(3)},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i, original := range messages {
		frame := eiRequest(original.object, original.opcode, original.body)
		// Deliberately split across header, size, opcode and payload boundaries.
		for len(frame) > 0 {
			n := min(1+i, len(frame))
			if _, err := serverConn.Write(frame[:n]); err != nil {
				t.Fatal(err)
			}
			frame = frame[n:]
		}
	}
	for _, want := range messages {
		actual, err := client.next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if actual.object != want.object || actual.opcode != want.opcode || !bytes.Equal(actual.body, want.body) {
			t.Fatalf("misordered or corrupted EIS packet got=%+v want=%+v", actual, want)
		}
	}
	if got := client.serial.Load(); got != 3 {
		t.Fatalf("serial after input state event %d, want 3", got)
	}
}

func TestEIClientRejectsPeerClosingMidProtocolFrame(t *testing.T) {
	for _, cut := range []int{1, 7, 11, 15, 16, 17, 20} {
		t.Run(strings.ReplaceAll(string(rune('a'+cut)), "/", "_"), func(t *testing.T) {
			conn, peer := unixConnectionPair(t)
			client := newEIClient(conn)
			defer client.close()
			frame := eiRequest(eiServerIDFloor+3, 6, append(eiU64(1), eiU64(2)...))
			if cut >= len(frame) {
				t.Fatal("invalid test cut")
			}
			if _, err := peer.Write(frame[:cut]); err != nil {
				t.Fatal(err)
			}
			if err := peer.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
				if client.readError() == nil {
					t.Fatal("truncated EIS stream appeared successful")
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if event, err := client.next(ctx); err == nil || event.object != 0 {
					t.Fatalf("partial EIS message leaked as valid event=%+v error=%v", event, err)
				}
			case <-time.After(time.Second):
				t.Fatal("partial EIS stream not closed")
			}
		})
	}
}

func TestEIClientCloseUnblocksReaderWhenInboundEventQueueIsFull(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	frame := eiRequest(eiServerIDFloor+1, 5, eiU32(1))
	payload := bytes.Repeat(frame, 180)
	if _, err := peer.Write(payload); err != nil {
		t.Fatal(err)
	}
	// Wait until the bounded queue fills, then terminate the reader while it
	// is blocked on enqueue rather than consuming any of those events.
	for len(client.messages) < cap(client.messages) {
		select {
		case <-ctx.Done():
			t.Fatal("EIS reader never filled bounded queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := client.close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	select {
	case <-client.done:
	case <-ctx.Done():
		t.Fatal("EIS reader did not exit after close with full queue")
	}
	if err := client.readError(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("unrecognized read termination: %v", err)
	}
}
