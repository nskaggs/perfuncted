package wl

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func newShutdownTestConnection(t *testing.T) (*Context, *net.UnixConn) {
	t.Helper()
	socket := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	peer, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	return client, peer
}

func TestRoundTripRejectsPeerThatCannotReceiveSyncAndUnregistersCallback(t *testing.T) {
	client, peer := newShutdownTestConnection(t)
	if err := peer.CloseRead(); err != nil {
		t.Fatal(err)
	}
	err := client.RoundTripContext(context.Background())
	if !errors.Is(err, syscall.EPIPE) {
		t.Fatalf("round trip to closed peer read side = %v, want EPIPE", err)
	}
	client.objectsMu.RLock()
	remaining := len(client.objects)
	client.objectsMu.RUnlock()
	if remaining != 0 {
		t.Fatalf("failed sync retained %d callback objects", remaining)
	}
}

type dispatchWatcherContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *dispatchWatcherContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestDispatchReportsReadAndDeadlineResetFailuresAfterLocalClose(t *testing.T) {
	client, _ := newShutdownTestConnection(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &dispatchWatcherContext{Context: ctx, observed: make(chan struct{})}
	result := make(chan error, 1)
	go func() { result <- client.dispatchWithCancellation(observed) }()
	select {
	case <-observed.observed:
	case <-time.After(time.Second):
		t.Fatal("dispatch cancellation watcher did not start")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) || !strings.Contains(err.Error(), "wl:") || !strings.Contains(err.Error(), "reset read deadline") {
			t.Fatalf("dispatch lost read or deadline-reset failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closed transport did not unblock dispatch")
	}
}
