package perfuncted

import (
	"context"
	"errors"
	"testing"
)

type cancelDuringManagedWindowSync struct {
	*handleWindowManager
	cancel context.CancelFunc
	calls  int
}

func (m *cancelDuringManagedWindowSync) Sync(context.Context) error {
	m.calls++
	if m.cancel != nil {
		m.cancel()
	}
	return nil
}

// A transport completing after caller cancellation cannot authorize the
// facade to claim that the requested synchronization completed successfully.
func TestWindowBundleSyncRejectsLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelDuringManagedWindowSync{handleWindowManager: newHandleWindowManager(), cancel: cancel}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	err := session.Windows.Sync(ctx)
	if !errors.Is(err, context.Canceled) || backend.calls != 1 {
		t.Fatalf("late cancel: err=%v backend calls=%d", err, backend.calls)
	}
}

func TestWindowBundleSyncRejectsPreCanceledOperation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &cancelDuringManagedWindowSync{handleWindowManager: newHandleWindowManager()}
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	err := session.Windows.Sync(ctx)
	if !errors.Is(err, context.Canceled) || backend.calls != 0 {
		t.Fatalf("pre-canceled sync: err=%v backend calls=%d", err, backend.calls)
	}
}
