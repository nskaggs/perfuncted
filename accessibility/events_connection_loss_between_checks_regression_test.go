package accessibility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type gateSecondEventContextCheck struct {
	context.Context
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (c *gateSecondEventContextCheck) Err() error {
	if c.calls.Add(1) == 2 {
		close(c.entered)
		<-c.release
	}
	return c.Context.Err()
}

// A private bus can disappear between the successful connectivity probe and
// dispatcher admission. The second check must reject that old connection
// without registering a dispatcher or publishing its startup state.
func TestEventStartRejectsConnectionLostBetweenConnectivityAndAdmission(t *testing.T) {
	backend := &dbusBackend{access: &dbus.Conn{}}
	ctx := &gateSecondEventContextCheck{
		Context: context.Background(),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	result := make(chan error, 1)
	go func() { result <- backend.startEvents(ctx, false) }()
	select {
	case <-ctx.entered:
	case <-time.After(3 * time.Second):
		close(ctx.release)
		t.Fatal("event setup did not reach second connection admission check")
	}
	backend.mu.Lock()
	backend.access = nil
	backend.mu.Unlock()
	close(ctx.release)
	select {
	case err := <-result:
		if !errors.Is(err, ErrDisconnected) {
			t.Fatalf("lost bus returned %v, want disconnected", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event setup hung after private bus vanished")
	}
	backend.eventsMu.Lock()
	defer backend.eventsMu.Unlock()
	if backend.eventStarting != nil || backend.eventAccess != nil || backend.eventCancel != nil || backend.eventStartStop != nil {
		t.Fatalf("vanished bus left event dispatcher state: starting=%v access=%v cancel=%v stop=%v", backend.eventStarting, backend.eventAccess, backend.eventCancel != nil, backend.eventStartStop != nil)
	}
}
