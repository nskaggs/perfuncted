package wl

import (
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
		s := ref.sess
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
	s := &Session{Sock: sock, Ctx: ctx, Display: d, Registry: r, Globals: make(map[string]GlobalEvent)}
	r.SetGlobalHandler(func(ev GlobalEvent) { s.Globals[ev.Interface] = ev })
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
		return ref.sess, nil
	}
	sessionCache[sock] = &sessionRef{sess: s, refs: 1}
	sessionCacheMu.Unlock()
	return s, nil
}

// Sync performs a synchronous wl_display.sync, pumping events until the
// sync callback is received. Mirrors Display.RoundTrip but operates on the
// Session's Display and Context.
func (s *Session) Sync() error {
	cb, err := s.Display.Sync()
	if err != nil {
		return err
	}
	done := make(chan struct{}, 1)
	cb.SetDoneHandler(func() { close(done) })
	for {
		if err := s.Ctx.Dispatch(); err != nil {
			return err
		}
		select {
		case <-done:
			return nil
		default:
		}
	}
}

// Close decrements the cached session's reference count and closes the
// underlying connection when it reaches zero.
func (s *Session) Close() error {
	sessionCacheMu.Lock()
	ref, ok := sessionCache[s.Sock]
	if !ok {
		sessionCacheMu.Unlock()
		// Not in cache: just close underlying ctx
		return s.Ctx.Close()
	}
	ref.refs--
	if ref.refs <= 0 {
		delete(sessionCache, s.Sock)
		sessionCacheMu.Unlock()
		return ref.sess.Ctx.Close()
	}
	sessionCacheMu.Unlock()
	return nil
}
