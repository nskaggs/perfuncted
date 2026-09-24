//go:build linux
// +build linux

package dbusutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHasServiceNil(t *testing.T) {
	if HasService(nil, "org.freedesktop.DBus") {
		t.Fatal("HasService(nil) = true, want false")
	}
}

func TestHasServiceContextRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if available, err := HasServiceContext(ctx, nil, "org.freedesktop.DBus"); available || !errors.Is(err, context.Canceled) {
		t.Fatalf("HasServiceContext() = (%v, %v), want (false, context.Canceled)", available, err)
	}
}

func TestSessionBusAddressInvalid(t *testing.T) {
	_, err := SessionBusAddress("unix:path=/tmp/nonexistent-dbus-socket-12345")
	if err == nil {
		t.Fatal("SessionBusAddress with invalid path succeeded, want error")
	}
}

func TestSessionBusAddressEmptyReturnsPrivateClosable(t *testing.T) {
	first, err := SessionBusAddress("")
	if err != nil {
		t.Skipf("no session bus available: %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := SessionBusAddress("")
	if err != nil {
		t.Fatalf("second SessionBusAddress() = %v", err)
	}
	defer func() { _ = second.Close() }()
	if first == second {
		t.Fatal("SessionBusAddress() returned the shared connection; want a private connection safe to Close")
	}
}

func TestRunHandshakeContextBoundsCancellationWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runHandshakeContext(ctx, func() error {
			close(started)
			<-release
			return nil
		}, func() error { return nil })
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handshake cancellation error = %v, want context.Canceled", err)
		}
	case <-time.After(handshakeCleanupTimeout + 200*time.Millisecond):
		t.Fatal("handshake cancellation waited beyond its cleanup bound")
	}
	close(release)
}
