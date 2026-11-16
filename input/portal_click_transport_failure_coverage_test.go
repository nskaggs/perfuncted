//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRemoteDesktopMouseClickMovementFailureRevokesWithoutRecordingInput(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	backend.sender.pointers = []eiPointerDevice{{
		device: 701, pointer: 702,
		regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}},
	}}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	err := backend.MouseClick(context.Background(), 20, 30, 1)
	if !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("click movement failure = %v, want revoked EIS transport error", err)
	}
	if backend.hasPointer || len(backend.buttons) != 0 {
		t.Fatalf("failed click movement recorded input: pointer=%v buttons=%v", backend.hasPointer, backend.buttons)
	}
	if stateErr := backend.stateError(); !errors.Is(stateErr, ErrRemoteDesktopRevoked) {
		t.Fatalf("failed click movement left authorization active: %v", stateErr)
	}
}
