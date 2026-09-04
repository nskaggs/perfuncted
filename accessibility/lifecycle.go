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
		err := fmt.Errorf("%w: target session address unavailable", ErrDisconnected)
		b.markDisconnected()
		return nil, err
	}
	fresh, err := openRuntime(runtime, generation+1)
	if err != nil {
		// A failed explicit reopen must not leave callers believing that the
		// previous transport is still a safe target. Invalidate and shut down
		// the old backend; the caller can retry Reopen explicitly later.
		b.markDisconnected()
		return nil, err
	}
	return fresh, nil
}

func (b *dbusBackend) startEvents(ctx context.Context) error {
	if err := b.connected(); err != nil {
		return err
	}
	// The physical stream belongs to the backend, not to the first caller's
	// subscription. Keep caller values but intentionally ignore that
	// subscriber's cancellation; watchSubscriber handles each stream itself.
	streamCtx := context.WithoutCancel(ctx)
	b.eventsMu.Lock()
	if b.eventCancel != nil {
		b.eventsMu.Unlock()
		return nil
	}
	access := b.access
	registry, registered := registerEvents(streamCtx, access)
	matches, err := subscribeEventMatches(streamCtx, access)
	if err != nil {
		deregisterEvents(streamCtx, registry, registered)
		b.eventsMu.Unlock()
		return err
	}
	signals := make(chan *dbus.Signal, 256)
	access.Signal(signals)
	eventCtx, cancel := context.WithCancel(streamCtx)
	done := make(chan struct{})
	b.eventCancel, b.eventDone = cancel, done
	b.eventsMu.Unlock()
	go func() {
		b.runEventDispatcher(eventCtx, access, signals, matches, registry, registered, done)
	}()
	return nil
}

func (b *dbusBackend) watchSubscriber(ctx context.Context, id uint64) {
	if ctx == nil || ctx.Done() == nil {
		// A non-cancelable context has no caller-owned lifetime to watch. The
		// dispatcher and backend Close still close this subscriber, so do not
		// strand a goroutine waiting forever on context.Background().
		return
	}
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
	defer func(cleanupCtx context.Context) {
		access.RemoveSignal(signals)
		for _, match := range matches {
			_ = access.RemoveMatchSignal(match...)
		}
		deregisterEvents(cleanupCtx, registry, registered)
		b.eventsMu.Lock()
		for id, subscriber := range b.subscribers {
			close(subscriber.out)
			delete(b.subscribers, id)
		}
		b.eventCancel, b.eventDone = nil, nil
		b.eventsMu.Unlock()
		close(done)
	}(context.WithoutCancel(ctx))
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
			// Every physical signal owns exactly one backend transition. Delivery
			// coalescing is deliberately evaluated only after invalidation/cache
			// handling so a dropped notification can never leave a fresh snapshot
			// looking current after a second signal.
			event = b.prepareEvent(sig, event)
			if coalesceEvent(&lastKey, &lastAt, event) {
				b.eventsMu.Lock()
				for _, subscriber := range b.subscribers {
					subscriber.dropped++
				}
				b.eventsMu.Unlock()
				continue
			}
			b.deliverEvent(event)
		}
	}
}

// prepareEvent performs the one invalidation/cache transition associated with
// a delivered physical signal and stamps the resulting event with a handle
// valid for the new generation.
func (b *dbusBackend) prepareEvent(sig *dbus.Signal, event Event) Event {
	// A cache signal carries an authoritative delta. Preserve the prior
	// upstream cache state across the generation transition, then apply that
	// delta under the new generation. Other signals deliberately discard cache
	// metadata so a changed name/state cannot leak into a fresh snapshot.
	var cacheItems map[NodeID]cacheItem
	var cacheApps map[string]bool
	if sig != nil && stringsHasCachePrefix(sig.Name) {
		b.mu.RLock()
		if b.cacheItems != nil {
			cacheItems = make(map[NodeID]cacheItem, len(b.cacheItems))
			for id, item := range b.cacheItems {
				cacheItems[id] = item
			}
		}
		if b.cacheApps != nil {
			cacheApps = make(map[string]bool, len(b.cacheApps))
			for name, present := range b.cacheApps {
				cacheApps[name] = present
			}
		}
		b.mu.RUnlock()
	}
	b.Invalidate(event.Node)
	if sig != nil && stringsHasCachePrefix(sig.Name) {
		b.mu.Lock()
		if cacheItems != nil {
			rekeyed := make(map[NodeID]cacheItem, len(cacheItems))
			for _, item := range cacheItems {
				rekeyed[item.nodeIDAt(b.generation)] = item
			}
			cacheItems = rekeyed
		}
		b.cacheItems, b.cacheApps = cacheItems, cacheApps
		b.mu.Unlock()
		b.applyCacheSignal(sig)
	}
	event.Node.Generation = b.Generation()
	return event
}

// deliverEvent is the single fan-out point. Holding eventsMu while sending
// keeps cancellation and close ordering race-free; bounded channels make the
// stream intentionally lossy for slow subscribers.
func (b *dbusBackend) deliverEvent(event Event) {
	if b == nil {
		return
	}
	b.eventsMu.Lock()
	defer b.eventsMu.Unlock()
	for _, subscriber := range b.subscribers {
		event.Dropped = subscriber.dropped
		select {
		case subscriber.out <- event:
			subscriber.dropped = 0
		default:
			subscriber.dropped++
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
