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
	closeErr     error
}

type accessibilityBackendOwner struct {
	session *Session

	mu          sync.Mutex
	current     *accessibilityBackendGeneration
	generations map[*accessibilityBackendGeneration]struct{}
	eventSubs   map[*accessibilityEventSubscription]struct{}
	closed      bool
	closeDone   chan struct{}
	closeErr    error

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
	ctx   context.Context //nolint:containedctx // the caller context owns the lifetime of this event stream.
	opts  accessibility.EventOptions
	out   chan accessibility.Event

	mu      sync.Mutex
	bound   *accessibilityBackendGeneration
	changed chan struct{}
	done    chan struct{}
	finish  sync.Once
}

func newAccessibilityBackendOwner(session *Session) *accessibilityBackendOwner {
	return &accessibilityBackendOwner{
		session:     session,
		generations: make(map[*accessibilityBackendGeneration]struct{}),
		eventSubs:   make(map[*accessibilityEventSubscription]struct{}),
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
		o := l.owner
		o.mu.Lock()
		l.gen.active--
		closeNow := l.gen.retired && l.gen.active == 0 && !l.gen.closeStarted
		if closeNow {
			l.gen.closeStarted = true
		}
		o.mu.Unlock()
		if closeNow {
			o.closeGeneration(l.gen)
		}
	})
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
	gen.closeErr = err
	close(gen.closeDone)
	o.mu.Unlock()
}

func (o *accessibilityBackendOwner) Close() error {
	if o == nil {
		return nil
	}
	o.epochMu.Lock()
	o.mu.Lock()
	if o.closed {
		done := o.closeDone
		o.mu.Unlock()
		o.epochMu.Unlock()
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
	current := o.current
	o.current = nil
	closeNow := o.retireLocked(current)
	gens := make([]*accessibilityBackendGeneration, 0, len(o.generations))
	for gen := range o.generations {
		gens = append(gens, gen)
	}
	done := o.closeDone
	o.mu.Unlock()
	o.epochMu.Unlock()
	if closeNow {
		o.closeGeneration(current)
	}
	for _, gen := range gens {
		<-gen.closeDone
	}
	errs := make([]error, 0, len(gens))
	for _, gen := range gens {
		if gen.closeErr != nil {
			errs = append(errs, gen.closeErr)
		}
	}
	err := errors.Join(errs...)
	o.mu.Lock()
	o.closeErr = err
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
		owner:   o,
		ctx:     ctx,
		opts:    opts,
		out:     make(chan accessibility.Event, opts.Buffer),
		changed: make(chan struct{}),
		done:    make(chan struct{}),
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
	o.mu.Unlock()
	gen, events, cancel, err := o.subscribeCurrent(ctx, opts, sub)
	if err != nil {
		sub.finishStream()
		return nil, err
	}
	go sub.pump(gen, events, cancel)
	return sub.out, nil
}

func (o *accessibilityBackendOwner) subscribeCurrent(ctx context.Context, opts accessibility.EventOptions, sub *accessibilityEventSubscription) (*accessibilityBackendGeneration, <-chan accessibility.Event, context.CancelFunc, error) {
	for {
		lease, err := o.acquire()
		if err != nil {
			return nil, nil, nil, err
		}
		gen := lease.gen
		source, ok := gen.backend.(accessibility.EventSource)
		if !ok {
			lease.release()
			return nil, nil, nil, accessibility.ErrUnsupported
		}
		sourceCtx, cancel := lease.context(ctx)
		events, sourceErr := source.Events(sourceCtx, opts)
		lease.release()
		if sourceErr != nil || events == nil {
			cancel()
			if sourceErr == nil {
				sourceErr = accessibility.ErrDisconnected
			}
			if !waitAccessibilityTransition(ctx, gen) {
				return nil, nil, nil, sourceErr
			}
			continue
		}

		o.mu.Lock()
		current := !o.closed && o.current == gen && !gen.retired
		if current {
			sub.markBound(gen)
		}
		o.mu.Unlock()
		if current {
			return gen, events, cancel, nil
		}
		cancel()
		if !waitAccessibilityTransition(ctx, gen) {
			return nil, nil, nil, accessibility.ErrDisconnected
		}
	}
}

func waitAccessibilityTransition(ctx context.Context, gen *accessibilityBackendGeneration) bool {
	select {
	case <-gen.retiredCh:
		return true
	default:
	}
	select {
	case <-gen.retiredCh:
		return true
	case <-ctx.Done():
		return false
	}
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

func (s *accessibilityEventSubscription) markBound(gen *accessibilityBackendGeneration) {
	s.mu.Lock()
	s.bound = gen
	close(s.changed)
	s.changed = make(chan struct{})
	s.mu.Unlock()
}

func (s *accessibilityEventSubscription) waitBound(ctx context.Context, gen *accessibilityBackendGeneration) {
	for {
		s.mu.Lock()
		if s.bound == gen {
			s.mu.Unlock()
			return
		}
		changed, done := s.changed, s.done
		s.mu.Unlock()
		select {
		case <-changed:
		case <-done:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *accessibilityEventSubscription) pump(gen *accessibilityBackendGeneration, events <-chan accessibility.Event, cancel context.CancelFunc) {
	defer s.finishStream()
	var dropped uint64
	rebind := func() bool {
		nextGen, nextEvents, nextCancel, err := s.owner.subscribeCurrent(s.ctx, s.opts, s)
		if err != nil {
			return false
		}
		gen, events, cancel = nextGen, nextEvents, nextCancel
		return true
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-gen.retiredCh:
			cancel()
			if !rebind() {
				return
			}
		case event, ok := <-events:
			if !ok {
				cancel()
				select {
				case <-gen.retiredCh:
					if !rebind() {
						return
					}
				case <-s.ctx.Done():
					return
				}
				continue
			}
			if !s.owner.isCurrent(gen) {
				continue
			}
			event.Dropped += dropped
			select {
			case s.out <- event:
				dropped = 0
			default:
				dropped++
			}
		}
	}
}

func (s *accessibilityEventSubscription) finishStream() {
	s.finish.Do(func() {
		owner := s.owner
		owner.mu.Lock()
		delete(owner.eventSubs, s)
		owner.mu.Unlock()
		s.mu.Lock()
		close(s.done)
		close(s.out)
		s.mu.Unlock()
	})
}

func (o *accessibilityBackendOwner) reopen(ctx context.Context) error {
	if ctx == nil {
		return errors.New("accessibility: nil context")
	}
	o.reopenMu.Lock()
	defer o.reopenMu.Unlock()
	lease, err := o.acquire()
	if err != nil {
		return err
	}
	defer lease.release()
	reopener, ok := lease.gen.backend.(accessibility.Reopener)
	if !ok {
		return accessibility.ErrUnsupported
	}
	reopenCtx, cancel := lease.context(ctx)
	fresh, err := reopener.Reopen(reopenCtx)
	cancel()
	if err != nil {
		return err
	}
	if util.IsNil(fresh) {
		return accessibility.ErrDisconnected
	}
	unlockEpoch, err := o.prepareReopenedBackend(lease, fresh)
	if err != nil {
		return err
	}
	newGen, subs, closeNow, err := o.publishReopenedBackend(lease, fresh)
	unlockEpoch()
	if err != nil {
		_ = fresh.Close()
		return err
	}
	if closeNow {
		o.closeGeneration(lease.gen)
	}
	waitCtx := o.session.ctx
	for _, sub := range subs {
		sub.waitBound(waitCtx, newGen) //nolint:contextcheck // session cancellation bounds rebinding after commit; caller cancellation cannot skip wake registration.
	}
	o.refreshCapabilities()
	o.session.notifyWaiters()
	return nil
}

func (o *accessibilityBackendOwner) prepareReopenedBackend(lease *accessibilityBackendLease, fresh accessibility.Backend) (func(), error) {
	o.epochMu.Lock()
	o.mu.Lock()
	if o.closed || o.current != lease.gen {
		o.mu.Unlock()
		o.epochMu.Unlock()
		_ = fresh.Close()
		if o.session != nil && o.session.isClosed() {
			return nil, ErrSessionClosed
		}
		return nil, accessibility.ErrDisconnected
	}
	o.mu.Unlock()

	if retire, ok := lease.gen.backend.(interface{ RetireEventsForReopen() }); ok {
		retire.RetireEventsForReopen()
	}
	if err := advanceReopenedGeneration(lease.gen.backend, fresh); err != nil {
		o.abandonCurrentGeneration(lease.gen, err)
		o.epochMu.Unlock()
		_ = fresh.Close()
		return nil, err
	}
	if o.session == nil {
		o.epochMu.Unlock()
		_ = fresh.Close()
		return nil, ErrNilSession
	}
	return o.epochMu.Unlock, nil
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

func (o *accessibilityBackendOwner) abandonCurrentGeneration(gen *accessibilityBackendGeneration, cause error) {
	o.mu.Lock()
	closeNow := false
	if o.current == gen {
		o.current = nil
		closeNow = o.retireLocked(gen)
	}
	closeNow = closeNow || (gen.retired && gen.active == 0 && !gen.closeStarted)
	if closeNow {
		gen.closeStarted = true
	}
	o.mu.Unlock()
	if closeNow {
		o.closeGeneration(gen)
	}
	if o.session != nil {
		o.session.capabilitiesMu.Lock()
		status := o.session.capabilities[CapabilityAccessibility]
		status.Available = false
		status.Failure = errors.Join(ErrUnavailable, cause)
		o.session.capabilities[CapabilityAccessibility] = status
		o.session.capabilitiesMu.Unlock()
	}
}

func (o *accessibilityBackendOwner) publishReopenedBackend(lease *accessibilityBackendLease, fresh accessibility.Backend) (*accessibilityBackendGeneration, []*accessibilityEventSubscription, bool, error) {
	session := o.session
	if session == nil {
		return nil, nil, false, ErrNilSession
	}
	session.lifecycleMu.Lock()
	defer session.lifecycleMu.Unlock()
	if session.closed || session.ctx == nil {
		return nil, nil, false, ErrSessionClosed
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.current != lease.gen {
		return nil, nil, false, accessibility.ErrDisconnected
	}
	newGen := newAccessibilityBackendGeneration(fresh)
	o.current = newGen
	o.generations[newGen] = struct{}{}
	closeNow := o.retireLocked(lease.gen)
	subs := make([]*accessibilityEventSubscription, 0, len(o.eventSubs))
	for sub := range o.eventSubs {
		subs = append(subs, sub)
	}
	return newGen, subs, closeNow, nil
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
	o.session.capabilitiesMu.Lock()
	o.session.capabilities[CapabilityAccessibility] = status
	o.session.capabilitiesMu.Unlock()
	lease.release()
}

var _ accessibility.Backend = (*accessibilityBackendOwner)(nil)
var _ accessibility.ApplicationFinder = (*accessibilityBackendOwner)(nil)
var _ accessibility.WindowResolver = (*accessibilityBackendOwner)(nil)
var _ accessibility.GenerationSource = (*accessibilityBackendOwner)(nil)
var _ accessibility.EventSource = (*accessibilityBackendOwner)(nil)
var _ accessibility.Automation = (*accessibilityBackendOwner)(nil)
