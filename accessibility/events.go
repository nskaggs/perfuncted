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

const observationHistoryLimit = 8192

type objectIdentity struct {
	busName    string
	objectPath string
}

func (id objectIdentity) valid() bool {
	return id.busName != "" && id.objectPath != ""
}

type observationChange struct {
	revision       uint64
	kind           string
	node           objectIdentity
	parent         objectIdentity
	removedChild   objectIdentity
	cacheAdd       bool
	cacheItem      cacheItem
	cacheRemove    bool
	childrenRemove bool
	unknown        bool
}

func (b *dbusBackend) Events(ctx context.Context, opts EventOptions) (<-chan Event, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if b == nil {
		return nil, ErrDisconnected
	}
	opts = opts.normalized()
	out := make(chan Event, opts.Buffer)
	for {
		if err := ctx.Err(); err != nil {
			close(out)
			return nil, err
		}
		if err := b.connected(); err != nil {
			close(out)
			return nil, err
		}
		id, start, wait, err := b.reserveEventSubscriber(out)
		if err != nil {
			close(out)
			return nil, err
		}
		if wait != nil {
			if err := waitForEventRun(ctx, wait); err != nil {
				close(out)
				return nil, err
			}
			continue
		}
		if start {
			if err := b.startEvents(ctx); err != nil {
				b.eventsMu.Lock()
				if subscriber, ok := b.subscribers[id]; ok {
					delete(b.subscribers, id)
					close(subscriber.out)
				}
				b.eventsMu.Unlock()
				return nil, err
			}
		}
		go b.watchSubscriber(ctx, id)
		return out, nil
	}
}

// reserveEventSubscriber admits a subscriber only when no zero-subscriber
// dispatcher run is still being torn down. The caller waits on the returned
// run completion channel without holding eventsMu, then retries admission.
func (b *dbusBackend) reserveEventSubscriber(out chan Event) (id uint64, start bool, wait <-chan struct{}, err error) {
	b.eventsMu.Lock()
	defer b.eventsMu.Unlock()
	if b.eventsRetired {
		return 0, false, nil, ErrDisconnected
	}
	if len(b.subscribers) == 0 {
		switch {
		case b.eventCancel != nil:
			if b.eventDone == nil {
				return 0, false, nil, errors.New("accessibility: event dispatcher has no completion signal")
			}
			return 0, false, b.eventDone, nil
		case b.eventStarting != nil:
			if b.eventStarting.done == nil {
				return 0, false, nil, errors.New("accessibility: event setup has no completion signal")
			}
			return 0, false, b.eventStarting.done, nil
		}
	}
	if b.subscribers == nil {
		b.subscribers = make(map[uint64]*eventSubscriber)
	}
	b.nextSubscriber++
	id = b.nextSubscriber
	b.subscribers[id] = &eventSubscriber{out: out}
	return id, b.eventCancel == nil, nil, nil
}

func waitForEventRun(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *dbusBackend) beginEventDispatcherCleanup(done chan struct{}) {
	b.eventsMu.Lock()
	if b.eventDone == done {
		for id, subscriber := range b.subscribers {
			close(subscriber.out)
			delete(b.subscribers, id)
		}
	}
	b.eventsMu.Unlock()
}

func (b *dbusBackend) finishEventDispatcher(done chan struct{}) {
	b.eventsMu.Lock()
	if b.eventDone == done {
		b.eventCancel, b.eventDone = nil, nil
		b.eventAccess = nil
	}
	// Wake admission waiters only after the old run is no longer published.
	close(done)
	b.eventsMu.Unlock()
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
		if b.eventsRetired {
			b.eventsMu.Unlock()
			return ErrDisconnected
		}
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
		live := !b.eventsRetired && b.connectedAccess(access) && len(b.subscribers) > 0
		if live {
			b.eventCancel, b.eventDone = cancel, done
			b.eventAccess = access
		}
		if b.eventStarting == state {
			if !live {
				state.err = ErrDisconnected
			} else {
				b.eventStarting = nil
				b.eventStartStop = nil
				close(state.done)
			}
		}
		b.eventsMu.Unlock()
		if !live {
			cancel()
			access.RemoveSignal(signals)
			cleanupCtx, cleanupCancel := eventCleanupContext() //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			removeEventMatches(cleanupCtx, access, matches)    //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			deregisterEvents(cleanupCtx, registry, registered) //nolint:contextcheck // cleanup is an independent bounded shutdown operation.
			cleanupCancel()
			b.finishEventStart(state, ErrDisconnected, ctx.Err() != nil)
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
	removed := false
	if subscriber, ok := b.subscribers[id]; ok {
		delete(b.subscribers, id)
		close(subscriber.out)
		removed = true
	}
	last := removed && len(b.subscribers) == 0
	cancel := b.eventCancel
	var startCancel context.CancelFunc
	if last && cancel == nil {
		startCancel = b.eventStartStop
	}
	b.eventsMu.Unlock()
	if last && cancel != nil {
		cancel()
	} else if last && startCancel != nil {
		startCancel()
	}
}

func (b *dbusBackend) runEventDispatcher(ctx context.Context, access *dbus.Conn, signals chan *dbus.Signal, matches [][]dbus.MatchOption, registry dbus.BusObject, registered []string, done chan struct{}) {
	defer func() { //nolint:contextcheck // dispatcher cleanup deliberately uses a bounded shutdown context.
		// Close admission before cleanup can block on remote deregistration. A
		// zero-subscriber run remains identifiable by its non-nil cancel/done
		// pair, so newcomers wait until the old physical stream is fully retired.
		b.beginEventDispatcherCleanup(done)

		cleanupCtx, cancel := eventCleanupContext()
		defer cancel()
		access.RemoveSignal(signals)
		removeEventMatches(cleanupCtx, access, matches)
		deregisterEvents(cleanupCtx, registry, registered)
		b.finishEventDispatcher(done)
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
				b.observationRevision++
				b.cacheRevision++
				b.cache, b.cacheItems, b.cacheApps = nil, nil, nil
				b.toolkits = nil
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
			var active bool
			event, active = b.prepareEventTransition(sig, event)
			if !active {
				continue
			}
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

// prepareEvent applies the state transition associated with one physical
// signal before delivery coalescing, then stamps the event with the current
// object-handle epoch.
func (b *dbusBackend) prepareEvent(sig *dbus.Signal, event Event) Event {
	prepared, _ := b.prepareEventTransition(sig, event)
	return prepared
}

func (b *dbusBackend) prepareEventTransition(sig *dbus.Signal, event Event) (Event, bool) {
	if b == nil {
		return event, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.disconnected {
		event.Node.Generation = b.generation
		return event, false
	}

	cacheSignal := sig != nil && stringsHasCachePrefix(sig.Name)
	items := b.cacheItems
	apps := b.cacheApps
	previousGeneration := b.generation
	b.cacheRevision++
	b.observationRevision++
	b.recordObservationLocked(sig, event, b.observationRevision)
	b.transitionObjectHandlesLocked(sig)
	if b.generation != previousGeneration {
		items, apps = nil, nil
	}
	b.cache, b.toolkits = nil, nil
	if cacheSignal {
		if items != nil {
			rekeyed := make(map[NodeID]cacheItem, len(items))
			for _, item := range items {
				ref := objectRef{BusName: item.Object.BusName, ObjectPath: item.Object.ObjectPath}
				rekeyed[b.nodeIDLocked(ref)] = item
			}
			items = rekeyed
		}
		b.cacheItems, b.cacheApps = items, apps
		b.applyCacheSignalLocked(sig)
	} else {
		b.cacheItems, b.cacheApps = nil, nil
	}
	event.Node = b.nodeIDLocked(objectRef{BusName: event.Node.BusName, ObjectPath: dbus.ObjectPath(event.Node.ObjectPath)})
	return event, true
}

func (b *dbusBackend) transitionObjectHandlesLocked(sig *dbus.Signal) {
	if sig == nil {
		return
	}
	name := sig.Name
	switch {
	case strings.HasSuffix(name, "Window:Destroy"):
		b.revokeOrInvalidateLocked(objectIdentity{busName: sig.Sender, objectPath: string(sig.Path)})
	case strings.HasSuffix(name, "Cache:RemoveAccessible"):
		b.transitionCacheRemovalLocked(sig)
	case strings.HasSuffix(name, "Object:ChildrenChanged"):
		b.transitionChildrenRemovalLocked(sig)
	case strings.HasSuffix(name, "Object:StateChanged"):
		b.transitionDefunctLocked(sig)
	}
}

func (b *dbusBackend) transitionCacheRemovalLocked(sig *dbus.Signal) {
	ref, ok := cacheObjectRefFromSignal(sig.Body)
	if !ok || ref.BusName != sig.Sender {
		b.invalidateAllHandlesLocked()
		return
	}
	b.revokeOrInvalidateLocked(objectIdentity{busName: ref.BusName, objectPath: string(ref.ObjectPath)})
}

func (b *dbusBackend) transitionChildrenRemovalLocked(sig *dbus.Signal) {
	if len(sig.Body) == 0 || sig.Body[0] != "remove" {
		return
	}
	ref, ok := childrenChangedObject(sig.Body)
	if !ok || ref.BusName != sig.Sender {
		b.invalidateAllHandlesLocked()
		return
	}
	b.revokeOrInvalidateLocked(objectIdentity{busName: ref.BusName, objectPath: string(ref.ObjectPath)})
}

func (b *dbusBackend) transitionDefunctLocked(sig *dbus.Signal) {
	if len(sig.Body) == 0 || sig.Body[0] != "defunct" {
		return
	}
	defunct, known := stateEnabled(sig.Body)
	if !known {
		b.invalidateAllHandlesLocked()
		return
	}
	if defunct {
		b.revokeOrInvalidateLocked(objectIdentity{busName: sig.Sender, objectPath: string(sig.Path)})
	}
}

func (b *dbusBackend) revokeOrInvalidateLocked(identity objectIdentity) {
	if !identity.valid() {
		b.invalidateAllHandlesLocked()
		return
	}
	if !b.revokeObjectLocked(identity) {
		b.invalidateAllHandlesLocked()
	}
}

func (b *dbusBackend) invalidateAllHandlesLocked() {
	b.generation++
	b.cache, b.cacheItems, b.cacheApps, b.toolkits = nil, nil, nil, nil
	b.incarnations, b.parents = nil, nil
	b.parentTrackingIncomplete = false
}

func childrenChangedObject(body []any) (cacheObjectRef, bool) {
	if len(body) != 5 {
		return cacheObjectRef{}, false
	}
	if _, ok := body[1].(int32); !ok {
		return cacheObjectRef{}, false
	}
	if _, ok := body[2].(int32); !ok {
		return cacheObjectRef{}, false
	}
	if _, ok := body[4].(map[string]dbus.Variant); !ok {
		return cacheObjectRef{}, false
	}
	return cacheObjectRefFromValue(body[3])
}

func stateEnabled(body []any) (bool, bool) {
	if len(body) != 5 {
		return false, false
	}
	if _, ok := body[2].(int32); !ok {
		return false, false
	}
	if _, ok := body[3].(dbus.Variant); !ok {
		return false, false
	}
	if _, ok := body[4].(map[string]dbus.Variant); !ok {
		return false, false
	}
	switch enabled := body[1].(type) {
	case int32:
		return enabled != 0, true
	case uint32:
		return enabled != 0, true
	case bool:
		return enabled, true
	default:
		return false, false
	}
}

func (b *dbusBackend) recordObservationLocked(sig *dbus.Signal, event Event, revision uint64) {
	change := observationChange{
		revision: revision,
		kind:     event.Kind,
		node:     objectIdentity{busName: event.Node.BusName, objectPath: event.Node.ObjectPath},
	}
	if sig == nil {
		change.unknown = true
	} else {
		switch {
		case strings.HasSuffix(sig.Name, "Cache:AddAccessible"):
			change.cacheAdd = true
			item, ok := cacheItemFromSignal(sig.Body)
			if !ok || item.Object.BusName != sig.Sender || item.Application.BusName != item.Object.BusName {
				change.unknown = true
			} else {
				change.cacheItem = item
				change.node = objectIdentity{busName: item.Object.BusName, objectPath: string(item.Object.ObjectPath)}
				change.parent = objectIdentity{busName: item.Parent.BusName, objectPath: string(item.Parent.ObjectPath)}
				if !change.node.valid() || !change.parent.valid() {
					change.unknown = true
				}
			}
		case strings.HasSuffix(sig.Name, "Cache:RemoveAccessible"):
			b.recordCacheRemovalObservationLocked(&change, sig)
		case strings.HasSuffix(sig.Name, "Object:ChildrenChanged") && len(sig.Body) > 0 && sig.Body[0] == "remove":
			b.recordChildrenRemovalObservationLocked(&change, sig)
		default:
			if !change.node.valid() {
				change.unknown = true
			}
		}
	}
	if len(b.observationHistory) == observationHistoryLimit {
		b.observationFloor = b.observationHistory[0].revision
		copy(b.observationHistory, b.observationHistory[1:])
		b.observationHistory = b.observationHistory[:len(b.observationHistory)-1]
	}
	b.observationHistory = append(b.observationHistory, change)
}

func (b *dbusBackend) recordCacheRemovalObservationLocked(change *observationChange, sig *dbus.Signal) {
	change.cacheRemove = true
	ref, ok := cacheObjectRefFromSignal(sig.Body)
	if !ok || ref.BusName != sig.Sender {
		change.unknown = true
		return
	}
	change.node = objectIdentity{busName: ref.BusName, objectPath: string(ref.ObjectPath)}
	id := b.nodeIDLocked(objectRef(ref))
	if item, exists := b.cacheItems[id]; exists {
		change.parent = objectIdentity{busName: item.Parent.BusName, objectPath: string(item.Parent.ObjectPath)}
	}
}

func (b *dbusBackend) recordChildrenRemovalObservationLocked(change *observationChange, sig *dbus.Signal) {
	change.childrenRemove = true
	ref, ok := childrenChangedObject(sig.Body)
	if !ok || ref.BusName != sig.Sender {
		change.unknown = true
		return
	}
	change.removedChild = objectIdentity{busName: ref.BusName, objectPath: string(ref.ObjectPath)}
}

func cacheItemFromSignal(body []any) (cacheItem, bool) { //nolint:gocyclo // every AT-SPI wire field is checked before cache state is published.
	if len(body) == 0 {
		return cacheItem{}, false
	}
	var item cacheItem
	switch value := body[0].(type) {
	case cacheItem:
		item = value
	case *cacheItem:
		if value == nil {
			return cacheItem{}, false
		}
		item = *value
	case []any:
		if len(value) != 10 {
			return cacheItem{}, false
		}
		object, objectOK := cacheObjectRefFromValue(value[0])
		application, applicationOK := cacheObjectRefFromValue(value[1])
		parent, parentOK := cacheObjectRefFromValue(value[2])
		index, indexOK := value[3].(int32)
		childCount, childCountOK := value[4].(int32)
		interfaces, interfacesOK := value[5].([]string)
		name, nameOK := value[6].(string)
		role, roleOK := value[7].(uint32)
		description, descriptionOK := value[8].(string)
		states, statesOK := value[9].([]uint32)
		if !objectOK || !applicationOK || !parentOK || !indexOK || !childCountOK || !interfacesOK || !nameOK || !roleOK || !descriptionOK || !statesOK {
			return cacheItem{}, false
		}
		item = cacheItem{
			Object: object, Application: application, Parent: parent,
			Index: index, ChildCount: childCount, Interfaces: interfaces,
			Name: name, Role: role, Description: description, States: states,
		}
	default:
		return cacheItem{}, false
	}
	if !validCacheObjectRef(item.Object) || !validCacheObjectRef(item.Application) || !validCacheObjectRef(item.Parent) || item.Object.BusName != item.Application.BusName {
		return cacheItem{}, false
	}
	return item, true
}

func cacheObjectRefFromSignal(body []any) (cacheObjectRef, bool) {
	if len(body) == 0 {
		return cacheObjectRef{}, false
	}
	return cacheObjectRefFromValue(body[0])
}

func cacheObjectRefFromValue(value any) (cacheObjectRef, bool) {
	switch ref := value.(type) {
	case dbus.Variant:
		return cacheObjectRefFromValue(ref.Value())
	case objectRef:
		converted := cacheObjectRef(ref)
		return converted, validCacheObjectRef(converted)
	case cacheObjectRef:
		return ref, validCacheObjectRef(ref)
	case []any:
		if len(ref) != 2 {
			return cacheObjectRef{}, false
		}
		busName, busOK := ref[0].(string)
		objectPath, pathOK := ref[1].(dbus.ObjectPath)
		if !pathOK {
			if path, ok := ref[1].(string); ok {
				objectPath = dbus.ObjectPath(path)
				pathOK = true
			}
		}
		decoded := cacheObjectRef{BusName: busName, ObjectPath: objectPath}
		if !busOK || !pathOK || !validCacheObjectRef(decoded) {
			return cacheObjectRef{}, false
		}
		return decoded, true
	default:
		return cacheObjectRef{}, false
	}
}

func validCacheObjectRef(ref cacheObjectRef) bool {
	return ref.BusName != "" && strings.TrimSpace(ref.BusName) == ref.BusName && !strings.ContainsAny(ref.BusName, " \t\r\n") && ref.ObjectPath.IsValid()
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
