package wl

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
)

func TestRegistryRejectsInterfaceNameMissingAlignmentPadding(t *testing.T) {
	var events []GlobalEvent
	registry := &Registry{}
	registry.SetGlobalHandler(func(event GlobalEvent) { events = append(events, event) })

	data := make([]byte, 10)
	PutUint32(data[0:4], 7)
	PutUint32(data[4:8], 2)
	data[8] = 'x'
	data[9] = 0
	registry.Dispatch(0, -1, data)
	if len(events) != 0 {
		t.Fatalf("truncated registry interface padding advertised a capability: %+v", events)
	}
}

func TestDispatchRejectsFrameWhosePayloadEndsEarly(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ctx, err := ConnectContext(context.Background(), socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ctx.Close() })
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}

	frame := make([]byte, 10)
	PutUint32(frame[0:4], 0)
	PutUint32(frame[4:8], 12<<16)
	frame[8], frame[9] = 1, 2
	if _, err := peer.Write(frame); err != nil {
		_ = peer.Close()
		t.Fatal(err)
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := ctx.DispatchContext(context.Background()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("dispatch of truncated Wayland frame = %v", err)
	}
}
