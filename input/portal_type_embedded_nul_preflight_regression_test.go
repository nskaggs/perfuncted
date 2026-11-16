//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

func TestRemoteDesktopTypeRejectsEmbeddedNULBeforeEarlierKeyFrames(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	err := backend.Type(context.Background(), "{enter}prefix\x00suffix")
	if err == nil || !strings.Contains(err.Error(), "embedded NUL") {
		t.Fatalf("Type with embedded NUL = %v, want text validation error", err)
	}
	assertNoEIWireRequest(t, peer)
	if len(backend.heldKeys) != 0 {
		t.Fatalf("rejected Type batch changed held keys: %v", backend.heldKeys)
	}
	if stateErr := backend.stateError(); stateErr != nil {
		t.Fatalf("local text validation revoked authorization: %v", stateErr)
	}
}
