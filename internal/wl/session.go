package wl

import (
	"context"
	"fmt"
	"sync"
)

// Session encapsulates a Wayland connection and the display/registry helpers.
// It performs a registry round-trip to populate Globals with advertised interfaces.
// Sessions are cached per-socket and reference-counted: calling NewSession will
// return a shared Session and increment its refcount; Close decrements the
// refcount and only closes the underlying connection when the last holder
// releases it.

type Session struct {
	Sock     string
	Ctx      *Context
	Display  *Display
	Registry *Registry
	Globals  map[string]GlobalEvent

	ref       *sessionRef
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
	canonical := &Session{Sock: sock, Ctx: ctx, Display: d, Registry: r, Globals: make(map[string]GlobalEvent)}
	r.SetGlobalHandler(func(ev GlobalEvent) { canonical.Globals[ev.Interface] = ev })
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
		Globals:  canonical.Globals,
		ref:      ref,
	}
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
	return WithOperationContext(ctx, s.Ctx, func() error {
		return s.Display.RoundTripContext(ctx)
	})
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
