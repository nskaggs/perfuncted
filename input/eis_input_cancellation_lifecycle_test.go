package input

import (
	"context"
	"errors"
	"testing"
)

func TestRemoteDesktopMouseClickCancellationRevokesHeldButton(t *testing.T) {
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- backend.MouseClick(ctx, 300, 400, 1) }()

	// Observe the authorized move and button press before canceling, so the
	// test proves teardown after the button has become an external side effect.
	assertSyntheticEISOperation(t, server, 101, 1)
	assertSyntheticEISOperation(t, server, 102, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled click error = %v, want caller cancellation", err)
	}
	<-backend.writerDone
	if err := backend.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("click cleanup left the authorized session usable: %v", err)
	}
}
