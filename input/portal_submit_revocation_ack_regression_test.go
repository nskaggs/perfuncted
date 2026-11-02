//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A successful transport receipt cannot reauthorize a command if the portal
// has already revoked the session before the receipt reaches its caller.
// Hold the synthetic writer at its acknowledgement boundary to reproduce the
// actual ordering without scheduler sleeps or a live authorized portal.
func TestRemoteDesktopSubmitDoesNotAcknowledgeReceiptAfterAuthorizationRevoked(t *testing.T) {
	b := syntheticQueuedPortal()
	admitted := make(chan struct{})
	acknowledge := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		cmd := <-b.commands
		close(admitted)
		<-acknowledge
		cmd.done <- nil
	}()
	result := make(chan error, 1)
	go func() { result <- b.submit(context.Background(), func() error { return nil }) }()
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("command was never admitted")
	}
	// revoke() publishes this state before it closes the revocation channel.
	// The synthetic seam isolates that precise interval without closing sockets.
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	close(acknowledge)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("revoked authorization acknowledged successful input: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not return after writer receipt")
	}
	<-writerDone
}

func TestRemoteDesktopSubmitDoesNotAcknowledgeReceiptAfterBackendStopped(t *testing.T) {
	b := syntheticQueuedPortal()
	admitted := make(chan struct{})
	acknowledge := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		cmd := <-b.commands
		close(admitted)
		<-acknowledge
		cmd.done <- nil
	}()
	result := make(chan error, 1)
	go func() { result <- b.submit(context.Background(), func() error { return nil }) }()
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("command was never admitted")
	}
	close(b.stop)
	close(acknowledge)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopUnavailable) {
			t.Fatalf("stopped backend acknowledged input: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not return after writer receipt")
	}
	<-writerDone
}

func TestRemoteDesktopSubmitPreservesTransportFailureWhenAuthorizationRevoked(t *testing.T) {
	b := syntheticQueuedPortal()
	admitted := make(chan struct{})
	acknowledge := make(chan struct{})
	writerDone := make(chan struct{})
	transportErr := errors.New("synthetic transport write rejected")
	go func() {
		defer close(writerDone)
		cmd := <-b.commands
		close(admitted)
		<-acknowledge
		cmd.done <- transportErr
	}()
	result := make(chan error, 1)
	go func() { result <- b.submit(context.Background(), func() error { return nil }) }()
	select {
	case <-admitted:
	case <-time.After(time.Second):
		t.Fatal("command was never admitted")
	}
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	close(acknowledge)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopRevoked) || !errors.Is(err, transportErr) {
			t.Fatalf("lost revoked or transport failure identity: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not return after writer receipt")
	}
	<-writerDone
}
