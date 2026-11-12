//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Transport failure is loss of the permission-bearing EIS session. A late
// caller must never see a successful input receipt or reuse that session.
func TestPortalEISSocketWriteFailureRevokesAuthorization(t *testing.T) {
	b, _, client := newSyntheticRevocableRemoteDesktop(t)
	if err := b.write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("real socket could not send first frame: %v", err)
	}
	if err := client.conn.Close(); err != nil {
		t.Fatal(err)
	}
	err := b.write([]byte{5, 6, 7, 8})
	if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("broken EIS write did not revoke authorization: %v", err)
	}
	if got := b.stateError(); !errors.Is(got, ErrRemoteDesktopRevoked) {
		t.Fatalf("broken EIS backend remained authorized: %v", got)
	}
	select {
	case <-b.revoked:
	case <-time.After(time.Second):
		t.Fatal("EIS failure did not notify authorization waiters")
	}
	if err := b.KeyDown(context.Background(), "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revoked session accepted keyboard input: %v", err)
	}
	select {
	case <-b.writerDone:
	case <-time.After(time.Second):
		t.Fatal("writer remained active after transport loss")
	}
}

// Closing the event stream without a reader-supplied error is still terminal:
// an absent diagnostic is not evidence that permission remains valid.
func TestPortalEISReadTerminationWithoutCauseRevokesAuthorization(t *testing.T) {
	b, _, client := newSyntheticRevocableRemoteDesktop(t)
	close(client.done)
	select {
	case <-b.revoked:
	case <-time.After(time.Second):
		t.Fatal("ended EIS stream did not retire the session")
	}
	if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS stream closed") {
		t.Fatalf("missing cause allowed EIS stream to remain authoritative: %v", err)
	}
}
