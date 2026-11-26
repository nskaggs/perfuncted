package sessionaccessibility

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"slices"
	"sync"
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

type Owner struct {
	session Admission

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
	owner *Owner
	gen   *accessibilityBackendGeneration
	once  sync.Once
}

type accessibilityEventSubscription struct {
	owner *Owner
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

func New(session Admission) *Owner {
	return &Owner{
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

func (o *Owner) Install(backend accessibility.Backend) {
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

func (o *Owner) acquire() (*accessibilityBackendLease, error) {
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

func (o *Owner) acquireGeneration(gen *accessibilityBackendGeneration) (*accessibilityBackendLease, error) {
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

func (o *Owner) HasBackend() bool {
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

func (o *Owner) releaseGeneration(gen *accessibilityBackendGeneration) {
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
	if l.owner.session != nil && l.owner.session.Context() != nil {
		stopSession = context.AfterFunc(l.owner.session.Context(), cancel)
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
	if l.owner.session != nil && l.owner.session.Context() != nil {
		stopSession = context.AfterFunc(l.owner.session.Context(), cancel)
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

func (o *Owner) withBackend(call func(accessibility.Backend) error) error {
	lease, err := o.acquire()
	if err != nil {
		return err
	}
	defer lease.release()
	return call(lease.gen.backend)
}

func (o *Owner) withBackendContext(ctx context.Context, call func(accessibility.Backend, context.Context) error) error {
	if ctx == nil {
		return errors.New("accessibility: nil context")
	}
	lease, err := o.acquire()
	if err != nil {
		return err
	}
	defer lease.release()
	callCtx, cancel := lease.context(ctx)
	defer cancel()
	if ctxErr := callCtx.Err(); ctxErr != nil {
		return ctxErr
	}
	if o.session != nil && o.session.Context() != nil {
		if sessionErr := o.session.Context().Err(); sessionErr != nil {
			return sessionErr
		}
	}
	err = call(lease.gen.backend, callCtx)
	// A provider may acknowledge a call after its *caller* has canceled.
	// Retirement of a backend generation, however, does not invalidate a
	// result from an already-admitted in-flight call: its lease pins that
	// generation until the operation completes.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	// Closing the owning session ends permission to receive private data,
	// even if a provider ignores cancellation and the original caller lives.
	// Generation retirement alone still permits admitted calls to finish.
	if o.session != nil && o.session.Context() != nil {
		if sessionErr := o.session.Context().Err(); sessionErr != nil {
			return sessionErr
		}
	}
	return err
}

func (o *Owner) retireLocked(gen *accessibilityBackendGeneration) bool {
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

func (o *Owner) closeGeneration(gen *accessibilityBackendGeneration) {
	err := gen.backend.Close()
	o.mu.Lock()
	delete(o.generations, gen)
	if err != nil {
		o.closeErrors = errors.Join(o.closeErrors, err)
	}
	close(gen.closeDone)
	o.mu.Unlock()
}

func (o *Owner) Close() error {
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

func (o *Owner) SupportedOperations() []string {
	var operations []string
	_ = o.withBackend(func(backend accessibility.Backend) error {
		operations = slices.Clone(backend.SupportedOperations())
		return nil
	})
	return operations
}

// A late provider success cannot publish an accessibility snapshot or node
// after the caller canceled the operation. Return only the cancellation error.
func discardCanceledAccessibilityResult[T any](value T, err error) (T, error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		var zero T
		return zero, err
	}
	return value, err
}

func (o *Owner) Applications(ctx context.Context) ([]accessibility.Application, error) {
	var result []accessibility.Application
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.Applications(callCtx)
		return err
	})
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) Snapshot(ctx context.Context, root accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	var result accessibility.Snapshot
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.Snapshot(callCtx, root, opts)
		return err
	})
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	var result []accessibility.Node
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.Find(callCtx, root, query, opts)
		return err
	})
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	var result accessibility.Node
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.Focused(callCtx, opts)
		return err
	})
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	var result accessibility.Node
	err := o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		var err error
		result, err = backend.AtPoint(callCtx, x, y)
		return err
	})
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
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
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) FindApplicationFresh(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
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
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) ResolveWindow(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
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
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) ResolveWindowFresh(ctx context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) {
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
	return discardCanceledAccessibilityResult(result, err)
}

func (o *Owner) Generation() uint64 {
	var generation uint64
	_ = o.withBackend(func(backend accessibility.Backend) error {
		if source, ok := backend.(accessibility.GenerationSource); ok {
			generation = source.Generation()
		}
		return nil
	})
	return generation
}

func (o *Owner) Invalidate(id accessibility.NodeID) {
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

func (o *Owner) Events(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
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
	if ctxErr := ctx.Err(); ctxErr != nil {
		cancel()
		return nil, ctxErr
	}
	// A retired backend generation can cancel its stream while the caller
	// remains live. A provider ignoring that cancellation must not hand an
	// obsolete channel containing private events to the new generation.
	if streamCtx.Err() != nil {
		cancel()
		return nil, accessibility.ErrDisconnected
	}
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

func (o *Owner) EventsAcrossReopens(ctx context.Context, opts accessibility.EventOptions) (<-chan accessibility.Event, error) {
	if ctx == nil {
		return nil, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
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
	if err := ctx.Err(); err != nil {
		o.mu.Unlock()
		return nil, err
	}
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

func (o *Owner) isCurrent(gen *accessibilityBackendGeneration) bool {
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
			// A provider event can be ready before caller cancellation, but
			// ownership validation may block until after cancellation.
			// Never publish that private buffered event to a retired caller.
			if err := ctx.Err(); err != nil {
				cancel()
				return nil, err
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
	// An event provider may return a buffered stream after ignoring caller
	// cancellation. Never acknowledge that stream to persistent binding
	// waiters or expose it to the forwarding loop.
	if ctxErr := ctx.Err(); ctxErr != nil {
		cancel()
		s.owner.setEventLimit(s, ctxErr)
		binding.complete(ctxErr)
		return nil, nil, ctxErr
	}
	if streamCtx.Err() != nil {
		cancel()
		s.owner.setEventLimit(s, accessibility.ErrDisconnected)
		binding.complete(accessibility.ErrDisconnected)
		return nil, nil, accessibility.ErrDisconnected
	}
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

func (o *Owner) Reopen(ctx context.Context) error {
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

func (o *Owner) openFreshBackend(ctx context.Context, lease *accessibilityBackendLease, reopener accessibility.Reopener) (accessibility.Backend, error) {
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

func (o *Owner) retireBeforeReopen(ctx context.Context, lease *accessibilityBackendLease) error {
	retireErr := o.retireEvents(ctx, lease)
	if retireErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if o.session == nil || o.session.IsClosed() {
			return capability.ErrSessionClosed
		}
		o.setAllEventLimits(retireErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return nil
}

func (o *Owner) publishFreshBackend(ctx context.Context, lease *accessibilityBackendLease, fresh accessibility.Backend) ([]*accessibilityEventBindingWaiter, bool, error) {
	o.epochMu.Lock()
	defer o.epochMu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, false, ctxErr
	}
	o.mu.Lock()
	canPublish := !o.closed && o.current == lease.gen
	o.mu.Unlock()
	if !canPublish {
		if o.session != nil && o.session.IsClosed() {
			return nil, false, capability.ErrSessionClosed
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

func (o *Owner) finishReopen(ctx context.Context, bindings []*accessibilityEventBindingWaiter) error {
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
	o.session.NotifyWaiters()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if o.session.IsClosed() {
		return capability.ErrSessionClosed
	}
	return waitErr
}

func (o *Owner) retireEvents(ctx context.Context, lease *accessibilityBackendLease) error {
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

func (o *Owner) abandonCurrentGeneration(gen *accessibilityBackendGeneration, cause error) bool {
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
		o.session.MarkUnavailable(cause)
	}
	return closeNow
}

func (o *Owner) publishReopenedBackend(lease *accessibilityBackendLease, fresh accessibility.Backend) ([]*accessibilityEventBindingWaiter, bool, error) {
	session := o.session
	if session == nil {
		return nil, false, capability.ErrNilSession
	}
	unlock, err := session.LockPublication()
	if err != nil {
		return nil, false, err
	}
	defer unlock()
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

func (o *Owner) setEventLimit(sub *accessibilityEventSubscription, err error) {
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

func (o *Owner) setAllEventLimits(err error) {
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

func (o *Owner) refreshCapabilities() {
	if o == nil || o.session == nil {
		return
	}
	lease, err := o.acquire()
	if err != nil {
		return
	}
	backend := lease.gen.backend
	status := o.session.DescribeBackend(backend)
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
	o.session.PublishStatus(status)
	o.mu.Unlock()
	lease.release()
}

var _ accessibility.Backend = (*Owner)(nil)
var _ accessibility.ApplicationFinder = (*Owner)(nil)
var _ accessibility.WindowResolver = (*Owner)(nil)
var _ accessibility.GenerationSource = (*Owner)(nil)
var _ accessibility.EventSource = (*Owner)(nil)
var _ accessibility.Automation = (*Owner)(nil)

func (o *Owner) withAutomation(ctx context.Context, call func(accessibility.Automation, context.Context) error) error {
	return o.withBackendContext(ctx, func(backend accessibility.Backend, callCtx context.Context) error {
		automation, ok := backend.(accessibility.Automation)
		if !ok {
			return accessibility.ErrUnsupported
		}
		return call(automation, callCtx)
	})
}

func withAutomationResult[T any](ctx context.Context, o *Owner, call func(accessibility.Automation, context.Context) (T, error)) (T, error) {
	var result T
	err := o.withAutomation(ctx, func(automation accessibility.Automation, callCtx context.Context) error {
		var err error
		result, err = call(automation, callCtx)
		return err
	})
	return result, err
}

func (o *Owner) InvokeAction(ctx context.Context, id accessibility.NodeID, index int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.InvokeAction(ctx, id, index) })
}

func (o *Owner) InvokeActionByName(ctx context.Context, id accessibility.NodeID, name string) (accessibility.Action, error) {
	return withAutomationResult(ctx, o, func(a accessibility.Automation, callCtx context.Context) (accessibility.Action, error) {
		return a.InvokeActionByName(callCtx, id, name)
	})
}

func (o *Owner) InvokeDefaultAction(ctx context.Context, id accessibility.NodeID) (accessibility.Action, error) {
	return withAutomationResult(ctx, o, func(a accessibility.Automation, callCtx context.Context) (accessibility.Action, error) {
		return a.InvokeDefaultAction(callCtx, id)
	})
}

func (o *Owner) GrabFocus(ctx context.Context, id accessibility.NodeID) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.GrabFocus(ctx, id) })
}

func (o *Owner) ScrollTo(ctx context.Context, id accessibility.NodeID, kind accessibility.ScrollType) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.ScrollTo(ctx, id, kind) })
}

func (o *Owner) ScrollToPoint(ctx context.Context, id accessibility.NodeID, kind accessibility.CoordType, x, y int) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.ScrollToPoint(ctx, id, kind, x, y)
	})
}

func (o *Owner) SetPosition(ctx context.Context, id accessibility.NodeID, x, y int, kind accessibility.CoordType) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetPosition(ctx, id, x, y, kind) })
}

func (o *Owner) SetSize(ctx context.Context, id accessibility.NodeID, width, height int) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetSize(ctx, id, width, height) })
}

func (o *Owner) SetExtents(ctx context.Context, id accessibility.NodeID, x, y, width, height int, kind accessibility.CoordType) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.SetExtents(ctx, id, x, y, width, height, kind)
	})
}

func (o *Owner) SetValue(ctx context.Context, id accessibility.NodeID, value float64) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetValue(ctx, id, value) })
}

func (o *Owner) SetTextContents(ctx context.Context, id accessibility.NodeID, value string) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetTextContents(ctx, id, value) })
}

func (o *Owner) InsertText(ctx context.Context, id accessibility.NodeID, offset int32, value string) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.InsertText(ctx, id, offset, value)
	})
}

func (o *Owner) DeleteText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeleteText(ctx, id, start, end) })
}

func (o *Owner) CopyText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.CopyText(ctx, id, start, end) })
}

func (o *Owner) CutText(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.CutText(ctx, id, start, end) })
}

func (o *Owner) PasteText(ctx context.Context, id accessibility.NodeID, position int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.PasteText(ctx, id, position) })
}

func (o *Owner) SetCaretOffset(ctx context.Context, id accessibility.NodeID, offset int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SetCaretOffset(ctx, id, offset) })
}

func (o *Owner) SetTextSelection(ctx context.Context, id accessibility.NodeID, selection, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.SetTextSelection(ctx, id, selection, start, end)
	})
}

func (o *Owner) AddTextSelection(ctx context.Context, id accessibility.NodeID, start, end int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.AddTextSelection(ctx, id, start, end)
	})
}

func (o *Owner) RemoveTextSelection(ctx context.Context, id accessibility.NodeID, selection int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.RemoveTextSelection(ctx, id, selection)
	})
}

func (o *Owner) SetTextSelections(ctx context.Context, id accessibility.NodeID, selections []accessibility.DocumentTextSelection) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error {
		return a.SetTextSelections(ctx, id, selections)
	})
}

func (o *Owner) SelectChild(ctx context.Context, id accessibility.NodeID, index int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SelectChild(ctx, id, index) })
}

func (o *Owner) DeselectChild(ctx context.Context, id accessibility.NodeID, index int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeselectChild(ctx, id, index) })
}

func (o *Owner) SelectAll(ctx context.Context, id accessibility.NodeID) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SelectAll(ctx, id) })
}

func (o *Owner) ClearSelection(ctx context.Context, id accessibility.NodeID) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.ClearSelection(ctx, id) })
}

func (o *Owner) DeselectSelectedChild(ctx context.Context, id accessibility.NodeID) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeselectSelectedChild(ctx, id) })
}

func (o *Owner) SelectRow(ctx context.Context, id accessibility.NodeID, row int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SelectRow(ctx, id, row) })
}

func (o *Owner) DeselectRow(ctx context.Context, id accessibility.NodeID, row int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeselectRow(ctx, id, row) })
}

func (o *Owner) SelectColumn(ctx context.Context, id accessibility.NodeID, column int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.SelectColumn(ctx, id, column) })
}

func (o *Owner) DeselectColumn(ctx context.Context, id accessibility.NodeID, column int32) error {
	return o.withAutomation(ctx, func(a accessibility.Automation, ctx context.Context) error { return a.DeselectColumn(ctx, id, column) })
}

// Status is only the dynamic provider portion of the session capability status.
type Status struct {
	Backend     string
	Operations  []string
	Diagnostics []string
}

// Admission is the generation owner's narrow session-lifecycle seam. The
// lifecycle lock is acquired before the owner lock when publishing a new
// generation. The owner lock protects a status publication against retirement;
// status callbacks take only the capability-status lock, never the lifecycle lock.
type Admission interface {
	Context() context.Context
	IsClosed() bool
	LockPublication() (unlock func(), err error)
	NotifyWaiters()
	MarkUnavailable(error)
	DescribeBackend(accessibility.Backend) Status
	PublishStatus(Status)
}
