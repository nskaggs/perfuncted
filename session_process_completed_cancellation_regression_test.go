package perfuncted

import (
	"context"
	"errors"
	"testing"
)

// If the process has exited and the caller has already canceled, the wait
// must never randomly report a successful observation from the ready child.
func TestManagedSessionProcessWaitCompletedChildRespectsCancellation(t *testing.T) {
	done := make(chan struct{})
	close(done)
	proc := &managedSessionProcess{waitDone: done}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for n := 0; n < 300; n++ {
		if err := proc.wait(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("wait %d acknowledged completed child despite canceled caller: %v", n, err)
		}
	}
}

func TestManagedSessionProcessWaitPreservesCancellationCause(t *testing.T) {
	done := make(chan struct{})
	close(done)
	proc := &managedSessionProcess{waitDone: done}
	cause := errors.New("synthetic owner revoked child observation")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := proc.wait(ctx); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("process wait lost cancellation cause: %v", err)
	}
}
