package wl

import "fmt"

// Session encapsulates a Wayland connection and the display/registry helpers.
// It performs a registry round-trip to populate Globals with advertised interfaces.
type Session struct {
	Ctx      *Context
	Display  *Display
	Registry *Registry
	Globals  map[string]GlobalEvent
}

// NewSession connects to the Wayland socket at sock, creates a Display and
// Registry, performs a RoundTrip to populate Globals, and returns the Session.
func NewSession(sock string) (*Session, error) {
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
	s := &Session{Ctx: ctx, Display: d, Registry: r, Globals: make(map[string]GlobalEvent)}
	r.SetGlobalHandler(func(ev GlobalEvent) { s.Globals[ev.Interface] = ev })
	if err := d.RoundTrip(); err != nil {
		_ = ctx.Close()
		return nil, fmt.Errorf("wl: registry round-trip: %w", err)
	}
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

// Close closes the underlying Wayland context.
func (s *Session) Close() error { return s.Ctx.Close() }
