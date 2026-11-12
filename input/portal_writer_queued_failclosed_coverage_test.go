//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The writer is a second authorization and cancellation boundary. An event
// already queued before the caller is canceled or the portal is retired
// must never execute even when the channel delivers it successfully.
func TestRemoteDesktopWriterRejectsQueuedCommandsWithoutExecution(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
		revoke bool
		want   error
	}{
		{name: "canceled before dequeue", cancel: true, want: context.Canceled},
		{name: "authorization revoked before dequeue", revoke: true, want: ErrRemoteDesktopRevoked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			b.commands = make(chan remoteDesktopCommand, 1)
			b.writerDone = make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ran := false
			command := remoteDesktopCommand{ctx: ctx, done: make(chan error, 1), run: func() error { ran = true; return nil }}
			b.commands <- command
			if tc.cancel {
				cancel()
			}
			if tc.revoke {
				b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
			}
			go b.writeLoop()
			select {
			case err := <-command.done:
				if !errors.Is(err, tc.want) {
					t.Fatalf("queued command returned %v, want %v", err, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("queued writer command did not receive receipt")
			}
			if ran {
				t.Fatal("unauthorized queued command executed")
			}
			close(b.stop)
			select {
			case <-b.writerDone:
			case <-time.After(time.Second):
				t.Fatal("writer did not retire")
			}
		})
	}
}
