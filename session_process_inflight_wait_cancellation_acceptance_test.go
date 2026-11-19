package perfuncted

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Observe the precise point at which wait starts listening for cancellation.
// The child never exits, so the only permitted completion is caller revocation.
type enteringWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *enteringWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestManagedSessionProcessWaitRevokedWhileChildStillRunning(t *testing.T) {
	for attempt := 0; attempt < 30; attempt++ {
		childExited := make(chan struct{})
		process := &managedSessionProcess{waitDone: childExited}
		cause := errors.New("session ownership was revoked")
		parent, cancel := context.WithCancelCause(context.Background())
		observed := &enteringWaitContext{Context: parent, entered: make(chan struct{})}
		result := make(chan error, 1)
		go func() { result <- process.wait(observed) }()
		select {
		case <-observed.entered:
		case <-time.After(time.Second):
			cancel(cause)
			t.Fatal("process wait did not enter its cancellation selector")
		}
		cancel(cause)
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("wait attempt %d lost cancellation or cause: %v", attempt, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("wait attempt %d remained blocked after cancellation", attempt)
		}
		select {
		case <-childExited:
			t.Fatal("canceling wait modified the child completion signal")
		default:
		}
	}
}
