package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/util"
)

type accessibilityBackendGeneration struct {
	backend      accessibility.Backend
	ctx          context.Context //nolint:containedctx // generation cancellation retires every call and event subscription pinned to this backend.
	cancel       context.CancelFunc
	retired      bool
	retiredCh    chan struct{}
	active       int
	closeStarted bool
	closeDone    chan struct{}
}

type accessibilityBackendOwner struct {
	session *Session

	mu          sync.Mutex
	current     *accessibilityBackendGeneration
	generations map[*accessibilityBackendGeneration]struct{}
	eventSubs   map[*accessibilityEventSubscription]struct{}
	eventLimits map[*accessibilityEventSubscription]string
	closed      bool
	closeDone   chan struct{}
	closeErr    error
	closeErrors error

	epochMu  sync.Mutex
	reopenMu sync.Mutex
}

type accessibilityBackendLease struct {
	owner *accessibilityBackendOwner
	gen   *accessibilityBackendGeneration
	once  sync.Once
}

type accessibilityEventSubscription struct {
	owner *accessibilityBackendOwner
	opts  accessibility.EventOptions
	out   chan accessibility.Event

	mu      sync.Mutex
	binding *accessibilityEventBinding
	done    chan struct{}
	finish  sync.Once
}

type accessibilityEventBinding struct {
	gen  *accessibilityBackendGeneration
	done chan struct{}
	once sync.Once
	err  error
}

type accessibilityEventBindingWaiter struct {
	subscription *accessibilityEventSubscription
	binding      *accessibilityEventBinding
}

func newAccessibilityBackendOwner(session *Session) *accessibilityBackendOwner {
	return &accessibilityBackendOwner{
		session:     session,
		generations: make(map[*accessibilityBackendGeneration]struct{}),
		eventSubs:   make(map[*accessibilityEventSubscription]struct{}),
		eventLimits: make(map[*accessibilityEventSubscription]string),
	}
}

func newAccessibilityBackendGeneration(backend accessibility.Backend) *accessibilityBackendGeneration {
	ctx, cancel := context.WithCancel(context.Background())
	return &accessibilityBackendGeneration{
		backend:   backend,
		ctx:       ctx,
		cancel:    cancel,
		retiredCh: make(chan struct{}),
		closeDone: make(chan struct{}),
	}
}

func (o *accessibilityBackendOwner) install(backend accessibility.Backend) {
	if o == nil || util.IsNil(backend) {
		if !util.IsNil(backend) {
			_ = backend.Close()
		}
		return
	}
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		_ = backend.Close()
		return
	}
	if o.current != nil {
		o.mu.Unlock()
		_ = backend.Close()
		return
	}
	gen := newAccessibilityBackendGeneration(backend)
	o.current = gen
	o.generations[gen] = struct{}{}
	o.mu.Unlock()
}

func (o *accessibilityBackendOwner) acquire() (*accessibilityBackendLease, error) {
	if o == nil {
		return nil, accessibility.ErrDisconnected
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.current == nil || o.current.retired {
		return nil, accessibility.ErrDisconnected
	}
	o.current.active++
	return &accessibilityBackendLease{owner: o, gen: o.current}, nil
}

func (o *accessibilityBackendOwner) acquireGeneration(gen *accessibilityBackendGeneration) (*accessibilityBackendLease, error) {
	if o == nil || gen == nil {
		return nil, accessibility.ErrDisconnected
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.current != gen || gen.retired {
		return nil, accessibility.ErrDisconnected
	}
	gen.active++
	return &accessibilityBackendLease{owner: o, gen: gen}, nil
}

func (o *accessibilityBackendOwner) hasBackend() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	available := !o.closed && o.current != nil && !o.current.retired
	o.mu.Unlock()
	return available
}

func (l *accessibilityBackendLease) release() {
	if l == nil || l.owner == nil || l.gen == nil {
		return
	}
	l.once.Do(func() {
		l.owner.releaseGeneration(l.gen)
	})
}

func (o *accessibilityBackendOwner) releaseGeneration(gen *accessibilityBackendGeneration) {
	o.mu.Lock()
	gen.active--
	closeNow := gen.retired && gen.active == 0 && !gen.closeStarted
	if closeNow {
		gen.closeStarted = true
	}
	o.mu.Unlock()
	if closeNow {
		o.closeGeneration(gen)
	}
}

func (l *accessibilityBackendLease) context(parent context.Context) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	stopGeneration := context.AfterFunc(l.gen.ctx, cancel)
	var stopSession func() bool
	if l.owner.session != nil && l.owner.session.ctx != nil {
		stopSession = context.AfterFunc(l.owner.session.ctx, cancel)
	}
	return ctx, func() {
		stopGeneration()
		if stopSession != nil {
			stopSession()
		}
		cancel()
	}
}

func (l *accessibilityBackendLease) streamContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stopGeneration := context.AfterFunc(l.gen.ctx, cancel)
	var stopSession func() bool
	if l.owner.session != nil && l.owner.session.ctx != nil {
		stopSession = context.AfterFunc(l.owner.session.ctx, cancel)
	}
	go func() {
		<-ctx.Done()
		stopGeneration()
		if stopSession != nil {
			stopSession()
		}
	}()
	return ctx, cancel
}

func (o *accessibilityBackendOwner) withBackend(call func(accessibility.Backend) error) error {
	lease, err := o.acquire()
	if err != nil {
		return err
	}
	defer lease.release()
	return call(lease.gen.backend)
}

func (o *accessibilityBackendOwner) withBackendContext(ctx context.Context, call func(accessibility.Backend, context.Context) error) error {
	lease, err := o.acquire()
	if err != nil {
		return err
	}
	defer lease.release()
	callCtx, cancel := lease.context(ctx)
	defer cancel()
	return call(lease.gen.backend, callCtx)
}

func (o *accessibilityBackendOwner) retireLocked(gen *accessibilityBackendGeneration) bool {
	if gen == nil || gen.retired {
		return false
	}
	gen.retired = true
	close(gen.retiredCh)
	gen.cancel()
	if gen.active == 0 && !gen.closeStarted {
		gen.closeStarted = true
		return true
	}
	return false
}

func (o *accessibilityBackendOwner) closeGeneration(gen *accessibilityBackendGeneration) {
	err := gen.backend.Close()
	o.mu.Lock()
	delete(o.generations, gen)
	if err != nil {
		o.closeErrors = errors.Join(o.closeErrors, err)
	}
	close(gen.closeDone)
	o.mu.Unlock()
}

func (o *accessibilityBackendOwner) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	if o.closed {
		done := o.closeDone
		o.mu.Unlock()
		if done != nil {
			<-done
		}
		o.mu.Lock()
		err := o.closeErr
		o.mu.Unlock()
		return err
	}
	o.closed = true
	o.closeDone = make(chan struct{})
	o.current = nil
	gens := make([]*accessibilityBackendGeneration, 0, len(o.generations))
	holds := make([]*accessibilityBackendLease, 0, len(o.generations))
	for gen := range o.generations {
		gens = append(gens, gen)
		if !gen.closeStarted {
			gen.active++
			o.retireLocked(gen)
			holds = append(holds, &accessibilityBackendLease{owner: o, gen: gen})
		}
	}
	done := o.closeDone
	o.mu.Unlock()
	for _, hold := range holds {
		_ = o.retireEvents(context.Background(), hold)
		hold.release()
	}
	for _, gen := range gens {
		<-gen.closeDone
	}
	o.mu.Lock()
	o.closeErr = o.closeErrors
	err := o.closeErr
	close(done)
	o.mu.Unlock()
	return err
}

func (o *accessibilityBackendOwner) SupportedOperations() []string {
	var operations []string
	_ = o.withBackend(func(backend accessibility.Backend) error {
		operations = slices.Clone(backend.SupportedOperations())
		return nil
	})
	return operations
}

func (o *accessibilityBackendOwner) Applications(ctx context.Context) ([]accessibility.Application, error) {
	var result []accessibility.Application
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.Applications(callCtx)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	var result accessibility.Snapshot
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.Snapshot(callCtx, root, opts)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	var result []accessibility.Node
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.Find(callCtx, root, query, opts)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	var result accessibility.Node
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.Focused(callCtx, opts)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	var result accessibility.Node
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.AtPoint(callCtx, x, y)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	var result accessibility.Application
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		finder, ok := backend.(accessibility.ApplicationFinder)
		if !ok {
			return accessibility.ErrUnsupported
		}
		var err error
		result, err = finder.FindApplication(callCtx, filter)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	var result accessibility.Application
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		finder, ok := backend.(accessibility.FreshApplicationFinder)
		if !ok {
			return accessibility.ErrUnsupported
		}
		var err error
		result, err = finder.FindApplicationFresh(callCtx, filter)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) ResolveWindow(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	var result accessibility.WindowScope
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		resolver, ok := backend.(accessibility.WindowResolver)
		if !ok {
			return accessibility.ErrUnsupported
		}
		var err error
		result, err = resolver.ResolveWindow(callCtx, target)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) ResolveWindowFresh(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
	var result accessibility.WindowScope
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		resolver, ok := backend.(accessibility.FreshWindowResolver)
		if !ok {
			return accessibility.ErrUnsupported
		}
		var err error
		result, err = resolver.ResolveWindowFresh(callCtx, target)
		return err
	})
	return result, err
}

func (o *accessibilityBackendOwner) Generation() uint64 {
	var generation uint64
	_ = o.withBackend(func(backend accessibility.Backend) error {
		if source, ok := backend.(accessibility.GenerationSource); ok {
			generation = source.Generation()
		}
		return nil
	})
	return generation
}

func (o *accessibilityBackendOwner) Invalidate(id accessibility.NodeID) {
	if o == nil {
		return
	}
	o.epochMu.Lock()
	defer o.epochMu.Unlock()
	lease, err := o.acquire()
	if err != nil {
		return
	}
	defer lease.release()
	if source, ok := lease.gen.backend.(accessibility.GenerationSource); ok {
		source.Invalidate(id)
	}
}

func (o *accessibilityBackendOwner) Events(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	lease, err := o.acquire()
	if err != nil {
		return nil, err
	}
	defer lease.release()
	source, ok := lease.gen.backend.(accessibility.EventSource)
	if !ok {
		return nil, accessibility.ErrUnsupported
	}
	streamCtx, cancel := lease.streamContext(ctx)
	stream, err := source.Events(streamCtx, opts)
	if err != nil {
		cancel()
		return nil, err
	}
	if stream == nil {
		cancel()
		return nil, accessibility.ErrDisconnected
	}
	return stream, nil
}

func (o *accessibilityBackendOwner) eventsAcrossReopens(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if opts.Buffer <= 0 {
		opts.Buffer = 64
	}
	if opts.Buffer > 4096 {
		opts.Buffer = 4096
	}
	sub := &accessibilityEventSubscription{
		owner: o,
		opts:  opts,
		out:   make(chan accessibility.Event, opts.Buffer),
		done:  make(chan struct{}),
	}
	if o == nil {
		return nil, accessibility.ErrDisconnected
	}
	o.mu.Lock()
	if o.closed || o.current == nil {
		o.mu.Unlock()
		return nil, accessibility.ErrDisconnected
	}
	o.eventSubs[sub] = struct{}{}
	binding := sub.beginBinding(o.current)
	o.mu.Unlock()
	if binding == nil {
		sub.finishStream()
		return nil, accessibility.ErrDisconnected
	}
	go sub.pump(ctx, binding)
	return sub.out, nil
}

func (o *accessibilityBackendOwner) isCurrent(gen *accessibilityBackendGeneration) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	current := !o.closed && o.current == gen
	o.mu.Unlock()
	return current
}

func (s *accessibilityEventSubscription) beginBinding(gen *accessibilityBackendGeneration) *accessibilityEventBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return nil
	default:
	}
	if s.binding != nil && s.binding.gen == gen {
		return s.binding
	}
	s.binding = &accessibilityEventBinding{gen: gen, done: make(chan struct{})}
	return s.binding
}

func (b *accessibilityEventBinding) complete(err error) {
	if b == nil {
		return
	}
	b.once.Do(func() {
		b.err = err
		close(b.done)
	})
}

func (s *accessibilityEventSubscription) waitBinding(ctx context.Context, binding *accessibilityEventBinding) error {
	if binding == nil {
		return accessibility.ErrDisconnected
	}
	select {
	case <-binding.done:
		return binding.err
	case <-s.done:
		return accessibility.ErrDisconnected
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *accessibilityEventSubscription) pump(ctx context.Context, binding *accessibilityEventBinding) {
	defer s.finishStream()
	var dropped uint64
	for {
		gen := binding.gen
		events, cancel, err := s.openBinding(ctx, binding)
		if err != nil {
			binding, err = s.nextBinding(ctx, gen)
			if err != nil {
				return
			}
			continue
		}
		binding, err = s.forwardGeneration(ctx, binding, events, cancel, &dropped)
		if err != nil {
			return
		}
	}
}

func (s *accessibilityEventSubscription) nextBinding(ctx context.Context, previous *accessibilityBackendGeneration) (*accessibilityEventBinding, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-previous.retiredCh:
	}
	s.owner.mu.Lock()
	defer s.owner.mu.Unlock()
	if s.owner.closed || s.owner.current == nil || s.owner.current == previous {
		return nil, accessibility.ErrDisconnected
	}
	next := s.beginBinding(s.owner.current)
	if next == nil {
		return nil, accessibility.ErrDisconnected
	}
	return next, nil
}

func (s *accessibilityEventSubscription) forwardGeneration(ctx context.Context, binding *accessibilityEventBinding, events <-chan accessibility.Event, cancel context.CancelFunc, dropped *uint64) (*accessibilityEventBinding, error) {
	gen := binding.gen
	for {
		select {
		case <-ctx.Done():
			cancel()
			return nil, ctx.Err()
		case <-gen.retiredCh:
			cancel()
			return s.nextBinding(ctx, gen)
		case event, ok := <-events:
			if !ok {
				cancel()
				s.owner.setEventLimit(s, accessibility.ErrDisconnected)
				return s.nextBinding(ctx, gen)
			}
			if !s.owner.isCurrent(gen) {
				continue
			}
			event.Dropped += *dropped
			select {
			case s.out <- event:
				*dropped = 0
			default:
				*dropped++
			}
		}
	}
}

func (s *accessibilityEventSubscription) openBinding(ctx context.Context, binding *accessibilityEventBinding) (<-chan accessibility.Event, context.CancelFunc, error) {
	lease, err := s.owner.acquireGeneration(binding.gen)
	if err != nil {
		s.owner.setEventLimit(s, err)
		binding.complete(err)
		return nil, nil, err
	}
	source, ok := lease.gen.backend.(accessibility.EventSource)
	if !ok {
		lease.release()
		sourceErr := accessibility.ErrUnsupported
		s.owner.setEventLimit(s, sourceErr)
		binding.complete(sourceErr)
		return nil, nil, sourceErr
	}
	streamCtx, cancel := lease.context(ctx)
	events, err := source.Events(streamCtx, s.opts)
	lease.release()
	if err == nil && events == nil {
		err = accessibility.ErrDisconnected
	}
	if err != nil {
		cancel()
		s.owner.setEventLimit(s, err)
		binding.complete(err)
		return nil, nil, err
	}
	if !s.owner.isCurrent(lease.gen) {
		cancel()
		err = accessibility.ErrDisconnected
		s.owner.setEventLimit(s, err)
		binding.complete(err)
		return nil, nil, err
	}
	s.owner.setEventLimit(s, nil)
	binding.complete(nil)
	return events, cancel, nil
}

func (s *accessibilityEventSubscription) finishStream() {
	s.finish.Do(func() {
		owner := s.owner
		owner.mu.Lock()
		delete(owner.eventSubs, s)
		delete(owner.eventLimits, s)
		owner.mu.Unlock()
		s.mu.Lock()
		binding := s.binding
		close(s.done)
		close(s.out)
		s.mu.Unlock()
		if binding != nil {
			binding.complete(accessibility.ErrDisconnected)
		}
	})
}

func (o *accessibilityBackendOwner) reopen(ctx context.Context) error {
	if ctx == nil {
		return errors.New("accessibility: nil context")
	}
	o.reopenMu.Lock()
	lease, err := o.acquire()
	if err != nil {
		o.reopenMu.Unlock()
		return err
	}
	defer lease.release()
	defer o.reopenMu.Unlock()
	reopener, ok := lease.gen.backend.(accessibility.Reopener)
	if !ok {
		return accessibility.ErrUnsupported
	}
	fresh, err := o.openFreshBackend(ctx, lease, reopener)
	if err != nil {
		return err
	}
	if retireErr := o.retireBeforeReopen(ctx, lease); retireErr != nil {
		_ = fresh.Close()
		return retireErr
	}
	bindings, closeOld, err := o.publishFreshBackend(ctx, lease, fresh)
	if closeOld {
		o.closeGeneration(lease.gen)
	}
	if err != nil {
		_ = fresh.Close()
		return err
	}
	return o.finishReopen(ctx, bindings)
}

func (o *accessibilityBackendOwner) openFreshBackend(ctx context.Context, lease *accessibilityBackendLease, reopener accessibility.Reopener) (accessibility.Backend, error) {
	reopenCtx, cancel := lease.context(ctx)
	fresh, err := reopener.Reopen(reopenCtx)
	cancel()
	if err != nil {
		if !util.IsNil(fresh) {
			_ = fresh.Close()
		}
		return nil, err
	}
	if util.IsNil(fresh) {
		return nil, accessibility.ErrDisconnected
	}
	return fresh, nil
}

func (o *accessibilityBackendOwner) retireBeforeReopen(ctx context.Context, lease *accessibilityBackendLease) error {
	retireErr := o.retireEvents(ctx, lease)
	if retireErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if o.session == nil || o.session.isClosed() {
			return ErrSessionClosed
		}
		o.setAllEventLimits(retireErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return nil
}

func (o *accessibilityBackendOwner) publishFreshBackend(ctx context.Context, lease *accessibilityBackendLease, fresh accessibility.Backend) ([]*accessibilityEventBindingWaiter, bool, error) {
	o.epochMu.Lock()
	defer o.epochMu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, false, ctxErr
	}
	o.mu.Lock()
	canPublish := !o.closed && o.current == lease.gen
	o.mu.Unlock()
	if !canPublish {
		if o.session != nil && o.session.isClosed() {
			return nil, false, ErrSessionClosed
		}
		return nil, false, accessibility.ErrDisconnected
	}
	if err := advanceReopenedGeneration(lease.gen.backend, fresh); err != nil {
		closeOld := o.abandonCurrentGeneration(lease.gen, err)
		return nil, closeOld, err
	}
	bindings, closeNow, err := o.publishReopenedBackend(lease, fresh)
	return bindings, closeNow, err
}

func (o *accessibilityBackendOwner) finishReopen(ctx context.Context, bindings []*accessibilityEventBindingWaiter) error {
	var waitErr error
	for _, waiter := range bindings {
		if err := waiter.subscription.waitBinding(ctx, waiter.binding); err != nil {
			if ctx.Err() != nil {
				waitErr = ctx.Err()
				break
			}
		}
	}
	o.refreshCapabilities()
	o.session.notifyWaiters()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if o.session.isClosed() {
		return ErrSessionClosed
	}
	return waitErr
}

func (o *accessibilityBackendOwner) retireEvents(ctx context.Context, lease *accessibilityBackendLease) error {
	retirer, ok := lease.gen.backend.(interface {
		RetireEventsForReopen(context.Context) error
	})
	if !ok {
		return nil
	}
	retireCtx, cancel := lease.context(ctx)
	defer cancel()
	return retirer.RetireEventsForReopen(retireCtx)
}

func advanceReopenedGeneration(old, fresh accessibility.Backend) error {
	oldGeneration := uint64(0)
	if source, ok := old.(accessibility.GenerationSource); ok {
		oldGeneration = source.Generation()
	}
	source, ok := fresh.(accessibility.GenerationSource)
	if !ok || source.Generation() > oldGeneration {
		return nil
	}
	source.Invalidate(accessibility.NodeID{})
	if source.Generation() > oldGeneration {
		return nil
	}
	return fmt.Errorf("accessibility: reopened generation %d does not exceed retired generation %d: %w", source.Generation(), oldGeneration, accessibility.ErrStaleGeneration)
}

func (o *accessibilityBackendOwner) abandonCurrentGeneration(gen *accessibilityBackendGeneration, cause error) bool {
	o.mu.Lock()
	closeNow := false
	abandoned := false
	if o.current == gen {
		o.current = nil
		abandoned = true
		closeNow = o.retireLocked(gen)
	}
	closeNow = closeNow || (gen.retired && gen.active == 0 && !gen.closeStarted)
	if closeNow {
		gen.closeStarted = true
	}
	o.mu.Unlock()
	if abandoned && o.session != nil {
		o.session.capabilitiesMu.Lock()
		status := o.session.capabilities[CapabilityAccessibility]
		status.Available = false
		status.Failure = errors.Join(ErrUnavailable, cause)
		o.session.capabilities[CapabilityAccessibility] = status
		o.session.capabilitiesMu.Unlock()
	}
	return closeNow
}

func (o *accessibilityBackendOwner) publishReopenedBackend(lease *accessibilityBackendLease, fresh accessibility.Backend) ([]*accessibilityEventBindingWaiter, bool, error) {
	session := o.session
	if session == nil {
		return nil, false, ErrNilSession
	}
	session.lifecycleMu.Lock()
	defer session.lifecycleMu.Unlock()
	if session.closed || session.ctx == nil {
		return nil, false, ErrSessionClosed
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.current != lease.gen {
		return nil, false, accessibility.ErrDisconnected
	}
	newGen := newAccessibilityBackendGeneration(fresh)
	o.current = newGen
	o.generations[newGen] = struct{}{}
	closeNow := o.retireLocked(lease.gen)
	bindings := make([]*accessibilityEventBindingWaiter, 0, len(o.eventSubs))
	for sub := range o.eventSubs {
		binding := sub.beginBinding(newGen)
		if binding != nil {
			bindings = append(bindings, &accessibilityEventBindingWaiter{subscription: sub, binding: binding})
		}
	}
	return bindings, closeNow, nil
}

func (o *accessibilityBackendOwner) setEventLimit(sub *accessibilityEventSubscription, err error) {
	o.mu.Lock()
	if _, active := o.eventSubs[sub]; active {
		if err == nil {
			delete(o.eventLimits, sub)
		} else {
			message := err.Error()
			if len(message) > 256 {
				message = message[:256]
			}
			o.eventLimits[sub] = "accessibility events unavailable: " + message
		}
	}
	o.mu.Unlock()
	o.refreshCapabilities()
}

func (o *accessibilityBackendOwner) setAllEventLimits(err error) {
	o.mu.Lock()
	for sub := range o.eventSubs {
		message := err.Error()
		if len(message) > 256 {
			message = message[:256]
		}
		o.eventLimits[sub] = "accessibility events unavailable: " + message
	}
	o.mu.Unlock()
	o.refreshCapabilities()
}

func (o *accessibilityBackendOwner) refreshCapabilities() {
	if o == nil || o.session == nil {
		return
	}
	lease, err := o.acquire()
	if err != nil {
		return
	}
	backend := lease.gen.backend
	status := o.session.Capability(CapabilityAccessibility)
	status.Available = true
	status.Failure = nil
	status.Backend = fmt.Sprintf("%T", backend)
	status.Operations = slices.Clone(supportedOperations(CapabilityAccessibility, backend))
	status.Diagnostics = backendDiagnostics(backend)
	o.mu.Lock()
	if o.closed || o.current != lease.gen {
		o.mu.Unlock()
		lease.release()
		return
	}
	limits := make([]string, 0, len(o.eventLimits))
	for _, limit := range o.eventLimits {
		limits = append(limits, limit)
	}
	slices.Sort(limits)
	status.Diagnostics = append(status.Diagnostics, limits...)
	o.session.capabilitiesMu.Lock()
	o.session.capabilities[CapabilityAccessibility] = status
	o.session.capabilitiesMu.Unlock()
	o.mu.Unlock()
	lease.release()
}

var _ accessibility.Backend = (*accessibilityBackendOwner)(nil)
var _ accessibility.ApplicationFinder = (*accessibilityBackendOwner)(nil)
var _ accessibility.WindowResolver = (*accessibilityBackendOwner)(nil)
var _ accessibility.GenerationSource = (*accessibilityBackendOwner)(nil)
var _ accessibility.EventSource = (*accessibilityBackendOwner)(nil)
var _ accessibility.Automation = (*accessibilityBackendOwner)(nil)
