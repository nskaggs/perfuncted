package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// An EIS writer may report success just as the compositor revokes its portal
// authorization. An accepted command receipt must never be returned as success
// once its session has been revoked.
func TestPortalSubmitRejectsSuccessfulReceiptAfterAuthorizationRevoked(t *testing.T) {
	b := syntheticQueuedPortal()
	received := make(chan remoteDesktopCommand, 1)
	go func() { received <- <-b.commands }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	var command remoteDesktopCommand
	select {
	case command = <-received:
	case <-ctx.Done():
		t.Fatal("command was not admitted")
	}
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	close(b.revoked)
	command.done <- nil
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("revoked portal returned successful receipt: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("revoked request did not finish")
	}
}

// A revoked authorization remains authoritative even if the queued writer
// completed with an unrelated I/O error. Both failure causes are useful to
// callers diagnosing why a previously authorized command failed.
func TestPortalSubmitJoinsTransportErrorWithLateRevocation(t *testing.T) {
	b := syntheticQueuedPortal()
	received := make(chan remoteDesktopCommand, 1)
	go func() { received <- <-b.commands }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	var command remoteDesktopCommand
	select {
	case command = <-received:
	case <-ctx.Done():
		t.Fatal("command was not admitted")
	}
	transportFailure := errors.New("synthetic EIS write rejected")
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	// Do not close the revoked notification yet: dispatch receipt must observe
	// the retired state independently of the channel notification.
	command.done <- transportFailure
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopRevoked) || !errors.Is(err, transportFailure) {
			t.Fatalf("lost revoked/transport error causes: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("revoked transport error did not finish")
	}
}
