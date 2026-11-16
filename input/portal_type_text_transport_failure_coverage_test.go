//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRemoteDesktopTypeTextTransportFailureRevokesAuthorization(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	err := backend.Type(context.Background(), "literal text")
	if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("Type text transport failure = %v, want revoked EIS transport error", err)
	}
	if stateErr := backend.stateError(); !errors.Is(stateErr, ErrRemoteDesktopRevoked) {
		t.Fatalf("failed text Type left authorization active: %v", stateErr)
	}
}
