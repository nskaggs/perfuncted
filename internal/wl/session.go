package wl

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"
	"sync"
)

// Session encapsulates a Wayland connection and the display/registry helpers.
// It performs a registry round-trip to populate its advertised-global snapshot.
// Sessions are cached per-socket and reference-counted: calling NewSession will
// return a shared Session and increment its refcount; Close decrements the
// refcount and only closes the underlying connection when the last holder
// releases it.

type Session struct {
	Sock     string
	Ctx      *Context
	Display  *Display
	Registry *Registry

	ref       *sessionRef
	globalsMu sync.RWMutex
	globals   []GlobalEvent
	closeMu   sync.Mutex
	closeDone bool
}

// sessionRef tracks a cached session and its reference count.
type sessionRef struct {
	sess *Session
	refs int
}

var (
	sessionCacheMu sync.Mutex
	sessionCache   = make(map[string]*sessionRef)
)

// NewSession returns a cached, reference-counted Session for sock. If no
// session exists, a new connection is established and cached. Call Close() on
// the returned Session to release the reference.
func NewSession(sock string) (*Session, error) {
	sessionCacheMu.Lock()
	if ref, ok := sessionCache[sock]; ok {
		ref.refs++
		s := newSessionHandle(ref)
		sessionCacheMu.Unlock()
		return s, nil
	}
	sessionCacheMu.Unlock()

	ctx, err := Connect(sock)
	if err != nil {
		return nil, fmt.Errorf("wl: connect: %w", err)
	}
	d := NewDisplay(ctx)
	r, err := d.GetRegistry()
	if err != nil {
		_ = ctx.Close()
		return nil, fmt.Errorf("wl: get registry: %w", err)
	}
	canonical := &Session{
		Sock:     sock,
		Ctx:      ctx,
		Display:  d,
		Registry: r,
		globals:  make([]GlobalEvent, 0),
	}
	r.SetGlobalHandler(canonical.addGlobal)
	r.SetGlobalRemoveHandler(canonical.removeGlobal)
	if err := d.RoundTrip(); err != nil {
		_ = ctx.Close()
		return nil, fmt.Errorf("wl: registry round-trip: %w", err)
	}

	sessionCacheMu.Lock()
	// Another goroutine may have created the session while we were dialing.
	if ref, ok := sessionCache[sock]; ok {
		ref.refs++
		sessionCacheMu.Unlock()
		// Close newly created ctx; use the existing cached session instead.
		_ = ctx.Close()
		return newSessionHandle(ref), nil
	}
	ref := &sessionRef{sess: canonical, refs: 1}
	canonical.ref = ref
	sessionCache[sock] = ref
	sessionCacheMu.Unlock()
	return newSessionHandle(ref), nil
}

func newSessionHandle(ref *sessionRef) *Session {
	canonical := ref.sess
	return &Session{
		Sock:     canonical.Sock,
		Ctx:      canonical.Ctx,
		Display:  canonical.Display,
		Registry: canonical.Registry,
		ref:      ref,
	}
}

// GlobalsSnapshot returns all globals currently advertised by the compositor,
// sorted by their stable registry name. The returned slice is independent of
// the session and safe for callers to retain or modify.
func (s *Session) GlobalsSnapshot() []GlobalEvent {
	canonical := s.canonical()
	if canonical == nil {
		return []GlobalEvent{}
	}
	canonical.globalsMu.RLock()
	defer canonical.globalsMu.RUnlock()
	return slices.Clone(canonical.globals)
}

func (s *Session) canonical() *Session {
	if s == nil {
		return nil
	}
	if s.ref != nil && s.ref.sess != nil {
		return s.ref.sess
	}
	return s
}

func (s *Session) addGlobal(ev GlobalEvent) {
	canonical := s.canonical()
	if canonical == nil {
		return
	}
	canonical.globalsMu.Lock()
	defer canonical.globalsMu.Unlock()
	for i, existing := range canonical.globals {
		if existing.Name == ev.Name {
			canonical.globals[i] = ev
			sortGlobals(canonical.globals)
			return
		}
	}
	canonical.globals = append(canonical.globals, ev)
	sortGlobals(canonical.globals)
}

func (s *Session) removeGlobal(name uint32) {
	canonical := s.canonical()
	if canonical == nil {
		return
	}
	canonical.globalsMu.Lock()
	defer canonical.globalsMu.Unlock()
	remaining := canonical.globals[:0]
	for _, ev := range canonical.globals {
		if ev.Name != name {
			remaining = append(remaining, ev)
		}
	}
	canonical.globals = remaining
}

func sortGlobals(globals []GlobalEvent) {
	slices.SortFunc(globals, func(a, b GlobalEvent) int {
		if c := cmp.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Interface, b.Interface); c != 0 {
			return c
		}
		return cmp.Compare(a.Version, b.Version)
	})
}

// Sync performs a synchronous wl_display.sync, pumping events until the
// sync callback is received. Mirrors Display.RoundTrip but operates on the
// Session's Display and Context.
func (s *Session) Sync() error {
	return s.SyncContext(context.Background())
}

// SyncContext performs a synchronous wl_display.sync that can be interrupted
// by ctx. It preserves the session's operation serialization for shared
// reference-counted connections.
func (s *Session) SyncContext(ctx context.Context) error {
	if s == nil || s.Display == nil {
		return nil
	}
	if s.isClosed() {
		return net.ErrClosed
	}
	return WithOperationContext(ctx, s.Ctx, func() error {
		return s.Display.RoundTripContext(ctx)
	})
}

func (s *Session) isClosed() bool {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	return s.closeDone
}

// Close decrements the cached session's reference count and closes the
// underlying connection when it reaches zero.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.closeMu.Lock()
	if s.closeDone {
		s.closeMu.Unlock()
		return nil
	}
	s.closeDone = true
	s.closeMu.Unlock()

	sessionCacheMu.Lock()
	ref, ok := sessionCache[s.Sock]
	if !ok || (s.ref != nil && ref != s.ref) || (s.ref == nil && ref.sess != s) {
		sessionCacheMu.Unlock()
		// The handle was already released, or it was never cached.
		if s.ref != nil || s.Ctx == nil {
			return nil
		}
		return s.Ctx.Close()
	}
	ref.refs--
	if ref.refs <= 0 {
		delete(sessionCache, s.Sock)
		sessionCacheMu.Unlock()
		if ref.sess.Ctx == nil {
			return nil
		}
		return ref.sess.Ctx.Close()
	}
	sessionCacheMu.Unlock()
	return nil
}
