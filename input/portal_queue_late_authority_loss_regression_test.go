package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Authorization can disappear after the caller's preflight but before the
// unbuffered writer queue accepts its command. The waiting submit must
// unblock without executing input or reporting successful dispatch.
func TestRemoteDesktopQueuedInputAbortsOnLateAuthorityLoss(t *testing.T) {
	for _, tc := range []struct {
		name   string
		retire func(*RemoteDesktopBackend)
		want   error
	}{
		{name: "portal revoked while waiting", retire: func(b *RemoteDesktopBackend) {
			b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
			close(b.revoked)
		}, want: ErrRemoteDesktopRevoked},
		{name: "backend stopped while waiting", retire: func(b *RemoteDesktopBackend) { close(b.stop) }, want: ErrRemoteDesktopUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			// No receiver is permitted until the retirement signal has arrived.
			// A submit that ignores that signal will hang rather than complete.
			result := make(chan error, 1)
			invoked := make(chan struct{}, 1)
			go func() { result <- b.submit(context.Background(), func() error { invoked <- struct{}{}; return nil }) }()
			tc.retire(b)
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Fatalf("retirement returned %v, expected %v", err, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("retired portal command remained blocked on writer")
			}
			select {
			case <-invoked:
				t.Fatal("unauthorized input ran")
			default:
			}
		})
	}
}

// Cancellation must win over a successful writer receipt when the writer
// reports success at the instant its request's authorization expires.
func TestRemoteDesktopQueuedInputLateCanceledReceipt(t *testing.T) {
	b := syntheticQueuedPortal()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan remoteDesktopCommand, 1)
	go func() { received <- <-b.commands }()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	var cmd remoteDesktopCommand
	select {
	case cmd = <-received:
	case <-time.After(time.Second):
		t.Fatal("writer never accepted input")
	}
	cancel()
	cmd.done <- nil
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled receipt falsely succeeded: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller blocked on writer acknowledgement")
	}
}
