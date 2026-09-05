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

func (b *dbusBackend) startEvents(ctx context.Context) error { //nolint:contextcheck,gocyclo // setup owns a bounded context derived from the caller.
	if ctx == nil {
		return errors.New("accessibility: nil context")
	}
	if err := b.connected(); err != nil {
		return err
	}
	// The physical stream belongs to the backend, not to the first caller's
	// subscription. Setup remains caller-cancellable; after setup, the
	// dispatcher is owned by the backend and is stopped by the last subscriber
	// or Close.
	retries := 0
	for {
		b.eventsMu.Lock()
		if b.eventCancel != nil {
			b.eventsMu.Unlock()
			return nil
		}
		if b.eventStarting != nil {
			state := b.eventStarting
			b.eventsMu.Unlock()
			connectedErr := b.connected()
			retry, err := waitForEventStart(ctx, state, connectedErr, retries)
			if retry {
				retries++
				continue
			}
			return err
		}
		state := &eventStart{done: make(chan struct{})}
		setupCtx, setupCancel := context.WithTimeout(ctx, eventSetupTimeout) //nolint:contextcheck // setup has an explicit bounded caller-derived deadline.
		access := b.eventConnection()
		if access == nil {
			b.eventsMu.Unlock()
			setupCancel()
			return ErrDisconnected
		}
		b.eventStarting = state
		b.eventStartStop = setupCancel
		b.eventAccess = access
		b.eventsMu.Unlock()

		registry, registered, err := registerEvents(setupCtx, access)
		var matches [][]dbus.MatchOption
		if err == nil {
			matches, err = subscribeEventMatches(setupCtx, access)
		}
		setupCancel()
		if err != nil {
			cleanupCtx, cancel := eventCleanupContext()        //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			removeEventMatches(cleanupCtx, access, matches)    //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			deregisterEvents(cleanupCtx, registry, registered) //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			cancel()
			callerCanceled := ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded))
			b.finishEventStart(state, err, callerCanceled)
			return err
		}

		signals := make(chan *dbus.Signal, 256)
		access.Signal(signals)
		eventCtx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		b.eventsMu.Lock()
		live := b.connectedAccess(access)
		if live {
			b.eventCancel, b.eventDone = cancel, done
			b.eventAccess = access
		}
		if b.eventStarting == state {
			if !live {
				state.err = ErrDisconnected
			}
			b.eventStarting = nil
			b.eventStartStop = nil
			close(state.done)
		}
		b.eventsMu.Unlock()
		if !live {
			cancel()
			cleanupCtx, cleanupCancel := eventCleanupContext() //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			removeEventMatches(cleanupCtx, access, matches)    //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			deregisterEvents(cleanupCtx, registry, registered) //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			cleanupCancel()
			return state.err
		}
		go func() {
			defer cancel()
			b.runEventDispatcher(eventCtx, access, signals, matches, registry, registered, done)
		}()
		return nil
	}
}

// waitForEventStart isolates a setup attempt's caller-owned cancellation from
// other subscribers. A healthy waiter may retry one canceled attempt, while a
// real provider failure is returned unchanged and retries stay bounded.
func waitForEventStart(ctx context.Context, state *eventStart, connectedErr error, retries int) (retry bool, err error) {
	select {
	case <-state.done:
		if state.err == nil {
			return false, nil
		}
		if state.callerCanceled && ctx.Err() == nil && connectedErr == nil && retries < eventStartRetryLimit {
			return true, nil
		}
		if state.callerCanceled && connectedErr != nil {
			return false, connectedErr
		}
		return false, state.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (b *dbusBackend) eventConnection() *dbus.Conn {
	if b == nil {
		return nil
	}
	b.mu.RLock()
	access := b.access
	b.mu.RUnlock()
	return access
}

func (b *dbusBackend) connectedAccess(access *dbus.Conn) bool {
	if b == nil || access == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return !b.closed && !b.disconnected && b.access == access
}

func (b *dbusBackend) finishEventStart(state *eventStart, err error, callerCanceled bool) {
	b.eventsMu.Lock()
	if b.eventStarting == state {
		state.err = err
		state.callerCanceled = callerCanceled
		b.eventStarting = nil
		b.eventStartStop = nil
		b.eventAccess = nil
		close(state.done)
	}
	b.eventsMu.Unlock()
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
	defer func() { //nolint:contextcheck // dispatcher cleanup deliberately uses a bounded shutdown context.
		cleanupCtx, cancel := eventCleanupContext()
		defer cancel()
		access.RemoveSignal(signals)
		removeEventMatches(cleanupCtx, access, matches)
		deregisterEvents(cleanupCtx, registry, registered)
		b.eventsMu.Lock()
		for id, subscriber := range b.subscribers {
			close(subscriber.out)
			delete(b.subscribers, id)
		}
		b.eventCancel, b.eventDone = nil, nil
		b.eventAccess = nil
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

func eventCleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), eventCleanupTimeout)
}

func (b *dbusBackend) stopEvents(access *dbus.Conn) {
	if b == nil {
		return
	}
	b.eventsMu.Lock()
	start, startCancel := b.eventStarting, b.eventStartStop
	cancel, done := b.eventCancel, b.eventDone
	if access == nil {
		access = b.eventAccess
	}
	b.eventsMu.Unlock()
	if startCancel != nil {
		startCancel()
	}
	if cancel != nil {
		cancel()
	}
	// Closing the private connection is the hard stop for a wedged D-Bus
	// reply. It also makes Close independent of remote deregistration health.
	if access != nil {
		_ = access.Close()
	}
	deadline := time.NewTimer(eventCleanupTimeout)
	defer deadline.Stop()
	if start != nil {
		select {
		case <-start.done:
		case <-deadline.C:
			return
		}
	}
	if done != nil {
		select {
		case <-done:
		case <-deadline.C:
		}
	}
}
