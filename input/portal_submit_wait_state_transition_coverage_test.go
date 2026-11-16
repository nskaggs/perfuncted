package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRemoteDesktopSubmitWakesWhenQueuedBackendStopsOrRevokes(t *testing.T) {
	for _, tc := range []struct {
		name string
		wake func(*RemoteDesktopBackend)
		want error
	}{
		{
			name: "authorization revoked",
			wake: func(b *RemoteDesktopBackend) {
				b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
				close(b.revoked)
			},
			want: ErrRemoteDesktopRevoked,
		},
		{
			name: "backend stopped",
			wake: func(b *RemoteDesktopBackend) { close(b.stop) },
			want: ErrRemoteDesktopUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := syntheticQueuedPortal()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			wait := &observedEISWaitContext{parent: ctx, started: make(chan struct{})}
			result := make(chan error, 1)
			go func() { result <- backend.submit(wait, func() error { return nil }) }()
			select {
			case <-wait.started:
			case <-ctx.Done():
				t.Fatal("submit did not enter its queue wait")
			}
			tc.wake(backend)
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Fatalf("submit after %s returned %v, want %v", tc.name, err, tc.want)
				}
			case <-ctx.Done():
				t.Fatalf("submit did not wake after %s", tc.name)
			}
		})
	}
}
