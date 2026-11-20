//go:build linux

package window

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

type observedX11IteratorContext struct {
	context.Context //nolint:containedctx // The wrapper exposes the initial cancellation check for synchronization.
	checked         chan struct{}
	once            sync.Once
}

func (c *observedX11IteratorContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestX11IteratorCancellationWhileWaitingForAdmission(t *testing.T) {
	backend, conn := newStubX11Backend(t, true, "private window")
	reads := 0
	conn.GetPropertyFunc = func(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie {
		reads++
		return x11.NewMockGetPropertyCookie(nil)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedX11IteratorContext{Context: parent, checked: make(chan struct{})}
	result := make(chan error, 1)
	backend.lifecycleMu.Lock()
	go func() {
		for info, err := range backend.IterateWindows(ctx) {
			if info != (Info{}) {
				result <- errors.New("canceled iterator disclosed a window")
				return
			}
			result <- err
			return
		}
		result <- errors.New("canceled iterator yielded no error")
	}()
	select {
	case <-ctx.checked:
		cancel()
		backend.lifecycleMu.Unlock()
	case <-time.After(time.Second):
		cancel()
		backend.lifecycleMu.Unlock()
		t.Fatal("iterator did not reach initial cancellation check")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iterator returned %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled iterator did not finish")
	}
	if reads != 0 {
		t.Fatalf("canceled iterator made %d X11 property requests", reads)
	}
}
