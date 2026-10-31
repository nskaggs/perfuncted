package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

func syntheticQueuedPortal() *RemoteDesktopBackend {
	return &RemoteDesktopBackend{sender: &eiSender{}, portal: &portalSession{}, commands: make(chan remoteDesktopCommand), stop: make(chan struct{}), revoked: make(chan struct{})}
}
func TestRemoteDesktopSubmitRejectsCanceledAndUnavailableQueue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*RemoteDesktopBackend)
		ctx   func() (context.Context, context.CancelFunc)
		want  error
	}{
		{"caller canceled before queue", func(*RemoteDesktopBackend) {}, func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, context.Canceled},
		{"caller deadline while queue blocked", func(*RemoteDesktopBackend) {}, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 10*time.Millisecond)
		}, context.DeadlineExceeded},
		{"backend stopped before queue", func(b *RemoteDesktopBackend) { close(b.stop) }, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Second)
		}, ErrRemoteDesktopUnavailable},
		{"authorization revoked before queue", func(b *RemoteDesktopBackend) {
			b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
			close(b.revoked)
		}, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Second)
		}, ErrRemoteDesktopRevoked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			tc.setup(b)
			ctx, cancel := tc.ctx()
			defer cancel()
			executed := false
			err := b.submit(ctx, func() error { executed = true; return nil })
			if !errors.Is(err, tc.want) || executed {
				t.Fatalf("queue accepted forbidden mutation: %v executed=%v", err, executed)
			}
		})
	}
}
func TestRemoteDesktopSubmitForwardsExactlyOneSerializedCommandReceipt(t *testing.T) {
	for _, failure := range []error{nil, errors.New("synthetic EIS transport failure")} {
		b := syntheticQueuedPortal()
		observed := make(chan struct{})
		completed := make(chan struct{})
		go func() {
			defer close(completed)
			command := <-b.commands
			if command.ctx.Err() != nil {
				command.done <- command.ctx.Err()
				return
			}
			if err := command.run(); err != nil {
				command.done <- err
				return
			}
			close(observed)
			command.done <- failure
		}()
		calls := 0
		err := b.submit(context.Background(), func() error { calls++; return nil })
		<-completed
		select {
		case <-observed:
		default:
			t.Fatal("accepted command not executed")
		}
		if calls != 1 || !errors.Is(err, failure) {
			t.Fatalf("command dispatch result=%v calls=%d want=%v", err, calls, failure)
		}
	}
}
func TestRemoteDesktopSubmitCancellationAfterAdmissionDoesNotAcknowledgeOperation(t *testing.T) {
	b := syntheticQueuedPortal()
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		command := <-b.commands
		close(received)
		<-release
		command.done <- nil // malicious/late provider success, caller is canceled
	}()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	<-received
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late provider success acknowledged despite cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled submit was blocked behind accepted request")
	}
	close(release)
	<-completed
}
func TestRemoteDesktopSubmitRejectsNilContextAndPartialState(t *testing.T) {
	b := syntheticQueuedPortal()
	if err := b.submit(absentPortalCommandContext, func() error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := (&RemoteDesktopBackend{}).submit(context.Background(), func() error { return nil }); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("partial portal queued input: %v", err)
	}
	if _, err := b.PointerCoordinateSpace(absentPortalCommandContext); err == nil {
		t.Fatal("nil query context accepted")
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentPortalCommandContext context.Context
