package wl

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func TestDispatchGrowsBufferForLargeWaylandEvent(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()
	ctx, err := ConnectContext(context.Background(), socket)
	if err != nil {
		t.Fatalf("ConnectContext: %v", err)
	}
	peer, err := listener.AcceptUnix()
	if err != nil {
		_ = ctx.Close()
		t.Fatalf("AcceptUnix: %v", err)
	}
	t.Cleanup(func() {
		_ = ctx.Close()
		_ = peer.Close()
	})

	payload := bytes.Repeat([]byte{0x5a}, 5000)
	proxy := &RawProxy{}
	dispatched := make(chan struct {
		opcode uint32
		length int
	}, 1)
	proxy.OnEvent = func(opcode uint32, _ int, data []byte) {
		dispatched <- struct {
			opcode uint32
			length int
		}{opcode: opcode, length: len(data)}
	}
	ctx.Register(proxy)

	message := make([]byte, 8+len(payload))
	PutUint32(message[0:4], proxy.ID())
	PutUint32(message[4:8], uint32(len(message))<<16|7)
	copy(message[8:], payload)
	if n, err := peer.Write(message); err != nil || n != len(message) {
		t.Fatalf("write large synthetic event: n=%d/%d err=%v", n, len(message), err)
	}
	if err := ctx.DispatchContext(context.Background()); err != nil {
		t.Fatalf("dispatch large synthetic event: %v", err)
	}
	select {
	case got := <-dispatched:
		if got.opcode != 7 || got.length != len(payload) {
			t.Fatalf("large event dispatch = opcode %d, payload %d; want opcode 7, payload %d", got.opcode, got.length, len(payload))
		}
	case <-time.After(time.Second):
		t.Fatal("large event did not reach its registered Wayland proxy")
	}
	if len(ctx.buf) < len(payload) {
		t.Fatalf("Wayland receive buffer remained %d bytes for %d-byte event", len(ctx.buf), len(payload))
	}
}
