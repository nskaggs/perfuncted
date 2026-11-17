package screen

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type extCaptureObservedDoneContext struct {
	context.Context
	doneCalled chan struct{}
	doneOnce   sync.Once
}

func (c *extCaptureObservedDoneContext) Done() <-chan struct{} {
	c.doneOnce.Do(func() { close(c.doneCalled) })
	return c.Context.Done()
}

// A capture canceled during operation admission must stop before touching
// session or shared-memory state.
func TestExtCaptureCancellationDuringAdmissionDoesNotStartCapture(t *testing.T) {
	backend := &ExtCaptureBackend{}
	backend.mu.Lock()
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &extCaptureObservedDoneContext{Context: base, doneCalled: make(chan struct{})}
	result := make(chan error, 1)
	go func() {
		_, err := backend.GrabFullHash(ctx)
		result <- err
	}()

	select {
	case <-ctx.doneCalled:
	case <-time.After(time.Second):
		backend.mu.Unlock()
		t.Fatal("capture did not reach context admission")
	}
	cancel()
	backend.mu.Unlock()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("capture after admission cancellation returned %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled capture did not leave the backend critical section")
	}

	if backend.session != nil || backend.shm != nil || backend.sessProxy != nil {
		t.Fatal("canceled uninitialized capture published protocol state")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close backend after canceled capture: %v", err)
	}
}
