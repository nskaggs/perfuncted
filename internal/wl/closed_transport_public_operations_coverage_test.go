package wl

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
)

// A closed Wayland transport cannot accept further writes, dispatches,
// round-trips or logical operations even though its proxy registry remains.
func TestWaylandClosedTransportRejectsEveryPublicOperation(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "w")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, err := ConnectContext(context.Background(), sock)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := ctx.Close(); err != nil {
		t.Fatal(err)
	}
	callbackCalled := false
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"write", func() error { return ctx.WriteMsgContext(context.Background(), []byte{0, 0, 0, 0}, nil) }},
		{"dispatch", func() error { return ctx.DispatchContext(context.Background()) }},
		{"round trip", func() error { return ctx.RoundTripContext(context.Background()) }},
		{"operation", func() error {
			return ctx.WithOperationContext(context.Background(), func() error { callbackCalled = true; return nil })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("closed Wayland transport accepted operation: %v", err)
			}
		})
	}
	if callbackCalled {
		t.Fatal("closed transport ran operation callback")
	}
}

func TestWaylandCanceledDialRejectsBeforeSocketAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if session, err := ConnectContext(ctx, "/not/a/real/socket"); session != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dial accessed filesystem: session=%v err=%v", session, err)
	}
}
