package accessibility

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
)

func (b *dbusBackend) connected() error {
	if b == nil {
		return ErrDisconnected
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.disconnected || b.closed || b.access == nil {
		return ErrDisconnected
	}
	return nil
}

// watchDisconnect observes the private bus only. It never attempts a
// reconnect; callers must explicitly invoke Reopen.
func (b *dbusBackend) watchDisconnect() {
	if b == nil || b.access == nil {
		return
	}
	go func(conn *dbus.Conn) {
		<-conn.Context().Done()
		b.markDisconnected()
	}(b.access)
}

func (b *dbusBackend) markDisconnected() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.disconnected {
		b.mu.Unlock()
		return
	}
	b.disconnected = true
	b.generation++
	b.cache, b.cacheItems, b.cacheApps = nil, nil, nil
	b.mu.Unlock()
	b.stopEvents(nil)
}

// Reopen creates a fresh backend against the same target session. The new
// generation is strictly greater than the old generation so no old handle
// can become valid again after an application or bus restart.
func (b *dbusBackend) Reopen(ctx context.Context) (Backend, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, ErrDisconnected
	}
	b.mu.RLock()
	runtime, generation := b.runtime, b.generation
	b.mu.RUnlock()
	if runtime.Get("DBUS_SESSION_BUS_ADDRESS") == "" {
		err := fmt.Errorf("%w: target session address unavailable", ErrDisconnected)
		b.markDisconnected()
		return nil, err
	}
	fresh, err := openRuntime(ctx, runtime, generation+1)
	if err != nil {
		// A failed explicit reopen must not leave callers believing that the
		// previous transport is still a safe target. Invalidate and shut down
		// the old backend; the caller can retry Reopen explicitly later.
		b.markDisconnected()
		return nil, err
	}
	return fresh, nil
}
