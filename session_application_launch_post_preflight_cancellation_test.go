package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type notifiedLaunchContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *notifiedLaunchContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

// A session launch can pass its initial context preflight and then wait for
// lifecycle ownership. Cancellation during that wait must prohibit spawning
// the executable even if it is otherwise valid and available.
func TestSessionLaunchRejectsCancellationWhileWaitingForLifecycleOwnership(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	marker := filepath.Join(t.TempDir(), "unexpected-child")
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &notifiedLaunchContext{Context: parent, checked: make(chan struct{})}
	session.lifecycleMu.Lock()
	lockHeld := true
	defer func() {
		if lockHeld {
			session.lifecycleMu.Unlock()
		}
	}()
	result := make(chan error, 1)
	go func() {
		app, err := session.Launch(ctx, Command{Name: "sh", Args: []string{"-c", "printf started > " + marker}})
		if app != nil {
			result <- errors.New("canceled launch returned a child")
			return
		}
		result <- err
	}()
	select {
	case <-ctx.checked:
	case <-time.After(2 * time.Second):
		t.Fatal("launch never passed its initial context preflight")
	}
	cancel()
	session.lifecycleMu.Unlock()
	lockHeld = false
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceling launch did not reject executable: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled launch remained blocked on lifecycle lock")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("executable started despite revoked authorization: %v", err)
	}
	if len(session.apps) != 0 {
		t.Fatalf("canceled launch retained %d children", len(session.apps))
	}
}
