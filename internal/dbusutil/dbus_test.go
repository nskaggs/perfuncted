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

func TestSessionBusAddressInvalid(t *testing.T) {
	_, err := SessionBusAddress("unix:path=/tmp/nonexistent-dbus-socket-12345")
	if err == nil {
		t.Fatal("SessionBusAddress with invalid path succeeded, want error")
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
