package input

import (
	"context"
	"errors"
	"testing"
)

func TestRemoteDesktopCoordinateQueryRespectsCanceledContext(t *testing.T) {
	b := &RemoteDesktopBackend{sender: &eiSender{}, portal: &portalSession{}, commands: make(chan remoteDesktopCommand), stop: make(chan struct{}), revoked: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.PointerCoordinateSpace(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("coordinate query on canceled ctx=%v want context.Canceled", err)
	}
}

func TestRemoteDesktopStateRevocationHasPriorityOverIncompleteSession(t *testing.T) {
	b := &RemoteDesktopBackend{}
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revoked state=%v", err)
	}
}
