package accessibility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/contextutil"
)

type eventSubscriber struct {
	out     chan Event
	dropped uint64
}

type eventStart struct {
	done           chan struct{}
	err            error
	callerCanceled bool
}

func (b *dbusBackend) Events(ctx context.Context, opts EventOptions) (<-chan Event, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if b == nil {
		return nil, ErrDisconnected
	}
	if err := b.connected(); err != nil {
		return nil, err
	}
	opts = opts.normalized()
	out := make(chan Event, opts.Buffer)
	b.eventsMu.Lock()
	b.nextSubscriber++
	id := b.nextSubscriber
	b.subscribers[id] = &eventSubscriber{out: out}
	start := b.eventCancel == nil
	b.eventsMu.Unlock()
	if start {
		if err := b.startEvents(ctx); err != nil {
			b.eventsMu.Lock()
			delete(b.subscribers, id)
			b.eventsMu.Unlock()
			close(out)
			return nil, err
		}
	}
	go b.watchSubscriber(ctx, id)
	return out, nil
}

func registerEvents(ctx context.Context, access *dbus.Conn) (dbus.BusObject, []string, error) {
	registered := []string{
		"object:property-change",
		"object:state-changed",
		"object:children-changed",
		"object:text-changed",
		"object:visible-data-changed",
		"focus:focus",
		"window:activate",
		"window:deactivate",
		"window:create",
		"window:destroy",
	}
	registry := access.Object(registryName, registryPath)
	unique := ""
	if names := access.Names(); len(names) > 0 {
		unique = names[0]
	}
	registered, err := registerEventFamilies(ctx, registry, unique, registered)
	if err != nil {
		return registry, registered, err
	}
	return registry, registered, nil
}

// registerEventFamilies tolerates providers that do not expose every AT-SPI
// event family. A stream remains usable when at least one family registers;
// the returned slice is the exact ownership record needed for cleanup.
func registerEventFamilies(ctx context.Context, registry eventRegistrar, unique string, eventTypes []string) ([]string, error) {
	registered := make([]string, 0, len(eventTypes))
	for _, eventType := range eventTypes {
		if err := registry.CallWithContext(ctx, registryName+".RegisterEvent", 0, eventType, []string{}, unique).Err; err != nil {
			continue
		}
		registered = append(registered, eventType)
	}
	if len(registered) == 0 {
		return nil, fmt.Errorf("accessibility: no AT-SPI event family registered: %w", ErrUnsupported)
	}
	return registered, nil
}

func subscribeEventMatches(ctx context.Context, access *dbus.Conn) ([][]dbus.MatchOption, error) {
	matches := [][]dbus.MatchOption{
		{dbus.WithMatchInterface("org.a11y.atspi.Event.Object")},
		{dbus.WithMatchInterface("org.a11y.atspi.Event.Focus")},
		{dbus.WithMatchInterface("org.a11y.atspi.Event.Window")},
		{dbus.WithMatchInterface(cacheIface)},
	}
	for i, match := range matches {
		if err := access.AddMatchSignalContext(ctx, match...); err != nil {
			return matches[:i], fmt.Errorf("accessibility: subscribe events: %w", err)
		}
	}
	return matches, nil
}

func removeEventMatches(ctx context.Context, access *dbus.Conn, matches [][]dbus.MatchOption) {
	if access == nil {
		return
	}
	for _, match := range matches {
		_ = access.RemoveMatchSignalContext(ctx, match...)
	}
}

type eventRegistrar interface {
	CallWithContext(context.Context, string, dbus.Flags, ...any) *dbus.Call
}

func deregisterEvents(ctx context.Context, registry eventRegistrar, registered []string) {
	if registry == nil {
		return
	}
	for _, eventType := range registered {
		_ = registry.CallWithContext(ctx, registryName+".DeregisterEvent", 0, eventType).Err
	}
}

func coalesceEvent(lastKey *string, lastAt *time.Time, event Event) bool {
	if lastKey == nil || lastAt == nil {
		return false
	}
	key := event.Kind + "\x00" + event.Node.BusName + "\x00" + event.Node.ObjectPath + "\x00" + event.Property
	if key == *lastKey && !lastAt.IsZero() && event.Timestamp.Sub(*lastAt) <= eventCoalesceWindow {
		return true
	}
	*lastKey, *lastAt = key, event.Timestamp
	return false
}

func signalEvent(sig *dbus.Signal) Event {
	event := Event{Timestamp: time.Now()}
	if sig == nil {
		return event
	}
	event.Kind = sig.Name
	event.Node = NodeID{BusName: sig.Sender, ObjectPath: string(sig.Path)}
	if len(sig.Body) > 0 {
		if property, ok := sig.Body[0].(string); ok {
			event.Property = property
		}
	}
	if len(sig.Body) > 1 {
		event.Value = fmt.Sprint(sig.Body[1])
		if sensitiveEventProperty(event.Property) {
			event.Value = ""
		}
	}
	return event
}

func sensitiveEventProperty(property string) bool {
	lower := strings.ToLower(strings.TrimSpace(property))
	return lower == "value" || strings.Contains(lower, "text") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "protected")
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
		setupCtx, setupCancel := eventSetupContext(ctx)
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

func eventSetupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return contextutil.WithTimeoutFallback(ctx, eventSetupTimeout)
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
