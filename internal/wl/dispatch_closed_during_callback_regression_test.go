package wl

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchReportsDeadlineResetFailureAfterCallbackClosesConnection(t *testing.T) {
	for _, cancelInCallback := range []bool{false, true} {
		name := "closed"
		if cancelInCallback {
			name = "closed and canceled"
		}
		t.Run(name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			client, err := ConnectContext(context.Background(), socket)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			peer, err := listener.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			proxy := &RawProxy{OnEvent: func(_ uint32, _ int, _ []byte) {
				if err := client.Close(); err != nil {
					t.Errorf("close during event: %v", err)
				}
				if cancelInCallback {
					cancel()
				}
			}}
			client.Register(proxy)
			var event [8]byte
			PutUint32(event[:4], proxy.ID())
			PutUint32(event[4:], 8<<16)
			if _, err := peer.Write(event[:]); err != nil {
				t.Fatal(err)
			}
			err = client.DispatchContext(ctx)
			if !errors.Is(err, net.ErrClosed) || !strings.Contains(err.Error(), "reset read deadline") {
				t.Fatalf("closed connection reset error = %v", err)
			}
			if cancelInCallback && !errors.Is(err, context.Canceled) {
				t.Fatalf("reset failure lost caller cancellation: %v", err)
			}
		})
	}
}
