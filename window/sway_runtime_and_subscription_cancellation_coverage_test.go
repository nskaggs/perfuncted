package window

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestNewSwayManagerReturnsCancellationFromRuntimeSocketProbe(t *testing.T) {
	runtimeDir := shortPrivateSocketDir(t)
	socket := filepath.Join(runtimeDir, "sway-ipc.test.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	dialEntered := make(chan struct{})
	releaseDial := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseDial) }) }
	originalDial := swayDialContext
	t.Cleanup(func() {
		release()
		swayDialContext = originalDial
	})
	swayDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialEntered)
		<-releaseDial
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		manager *SwayManager
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		manager, openErr := NewSwayManagerRuntimeContext(ctx, env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + runtimeDir}))
		resultCh <- result{manager: manager, err: openErr}
	}()

	select {
	case <-dialEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime discovery did not reach the advertised Sway socket")
	}
	cancel()
	release()

	select {
	case got := <-resultCh:
		if got.manager != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("runtime socket probe returned manager=%v err=%v, want context.Canceled", got.manager, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runtime discovery did not return after cancellation")
	}
}

func TestSwayCloseDuringSubscriptionDialClosesLateConnection(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	if err := peer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	dialEntered := make(chan struct{})
	releaseDial := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseDial) }) }
	originalDial := swayDialContext
	swayDialContext = func(context.Context, string, string) (net.Conn, error) {
		close(dialEntered)
		<-releaseDial
		return client, nil
	}

	manager := &SwayManager{sock: "synthetic"}
	t.Cleanup(func() {
		release()
		_ = manager.Close()
		swayDialContext = originalDial
	})
	events := manager.WindowChanges()
	select {
	case <-dialEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("Sway subscription did not begin dialing")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close() }()
	select {
	case <-manager.eventStop:
	case <-time.After(2 * time.Second):
		t.Fatal("manager close did not signal the pending subscription")
	}
	release()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("closing manager during subscription dial: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("manager close did not join the pending subscription")
	}
	if _, ok := <-events; ok {
		t.Fatal("closed manager left its event stream open")
	}
	var one [1]byte
	if _, err := peer.Read(one[:]); err == nil {
		t.Fatal("late subscription peer remained open after manager close")
	}
}
