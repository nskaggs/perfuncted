package accessibility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	b.stopEvents()
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
		return nil, fmt.Errorf("%w: target session address unavailable", ErrDisconnected)
	}
	return openRuntime(runtime, generation+1)
}

func (b *dbusBackend) startEvents(_ context.Context) error {
	if err := b.connected(); err != nil {
		return err
	}
	b.eventsMu.Lock()
	if b.eventCancel != nil {
		b.eventsMu.Unlock()
		return nil
	}
	access := b.access
	registry, registered := registerEvents(context.Background(), access)
	matches, err := subscribeEventMatches(context.Background(), access)
	if err != nil {
		deregisterEvents(context.Background(), registry, registered)
		for id, subscriber := range b.subscribers {
			close(subscriber.out)
			delete(b.subscribers, id)
		}
		b.eventsMu.Unlock()
		return err
	}
	signals := make(chan *dbus.Signal, 256)
	access.Signal(signals)
	eventCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	b.eventCancel, b.eventDone = cancel, done
	b.eventsMu.Unlock()
	go b.runEventDispatcher(eventCtx, access, signals, matches, registry, registered, done)
	return nil
}

func (b *dbusBackend) watchSubscriber(ctx context.Context, id uint64) {
	<-ctx.Done()
	b.eventsMu.Lock()
	if subscriber, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(subscriber.out)
	}
	last := len(b.subscribers) == 0
	cancel := b.eventCancel
	b.eventsMu.Unlock()
	if last && cancel != nil {
		cancel()
	}
}

func (b *dbusBackend) runEventDispatcher(ctx context.Context, access *dbus.Conn, signals chan *dbus.Signal, matches [][]dbus.MatchOption, registry dbus.BusObject, registered []string, done chan struct{}) {
	defer func() {
		access.RemoveSignal(signals)
		for _, match := range matches {
			_ = access.RemoveMatchSignal(match...)
		}
		deregisterEvents(context.Background(), registry, registered)
		b.eventsMu.Lock()
		for id, subscriber := range b.subscribers {
			close(subscriber.out)
			delete(b.subscribers, id)
		}
		b.eventCancel, b.eventDone = nil, nil
		b.eventsMu.Unlock()
		close(done)
	}()
	lastKey := ""
	var lastAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-access.Context().Done():
			b.mu.Lock()
			if !b.disconnected {
				b.disconnected = true
				b.generation++
				b.cache, b.cacheItems, b.cacheApps = nil, nil, nil
			}
			b.mu.Unlock()
			return
		case sig, ok := <-signals:
			if !ok {
				return
			}
			event := signalEvent(sig)
			event.Node.Generation = b.Generation()
			if coalesceEvent(&lastKey, &lastAt, event) {
				b.eventsMu.Lock()
				for _, subscriber := range b.subscribers {
					subscriber.dropped++
				}
				b.eventsMu.Unlock()
				continue
			}
			if stringsHasCachePrefix(sig.Name) {
				b.applyCacheSignal(sig)
			}
			b.Invalidate(event.Node)
			b.eventsMu.Lock()
			for _, subscriber := range b.subscribers {
				event.Dropped = subscriber.dropped
				select {
				case subscriber.out <- event:
					subscriber.dropped = 0
				default:
					subscriber.dropped++
				}
			}
			b.eventsMu.Unlock()
		}
	}
}

func stringsHasCachePrefix(name string) bool {
	return strings.HasPrefix(name, cacheIface+":") || strings.Contains(name, ".Cache:")
}

func (b *dbusBackend) stopEvents() {
	if b == nil {
		return
	}
	b.eventsMu.Lock()
	cancel, done := b.eventCancel, b.eventDone
	b.eventsMu.Unlock()
	if cancel != nil {
		cancel()
		if done != nil {
			<-done
		}
	}
}
