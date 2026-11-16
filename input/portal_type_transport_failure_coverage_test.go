//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRemoteDesktopTypeTransportFailureDoesNotRecordKeyOwnership(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	err := backend.Type(context.Background(), "{a}")
	if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("Type transport failure = %v, want revoked EIS transport error", err)
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("failed key frame recorded held-key ownership: %v", backend.heldKeys)
	}
	if stateErr := backend.stateError(); !errors.Is(stateErr, ErrRemoteDesktopRevoked) {
		t.Fatalf("failed Type left authorization active: %v", stateErr)
	}
}
