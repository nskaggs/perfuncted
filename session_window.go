package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nskaggs/perfuncted/v2/internal/sessionwindow"

	"github.com/nskaggs/perfuncted/v2/window"
)

var (
	// ErrWindowNotFound indicates that no window satisfies a match.
	ErrWindowNotFound = window.ErrWindowNotFound
	// ErrWindowAmbiguous indicates that a match satisfies multiple windows.
	ErrWindowAmbiguous = window.ErrWindowAmbiguous
	// ErrApplicationExited indicates that an application exited before the
	// requested window appeared.
	ErrApplicationExited = errors.New("application exited before window appeared")
)

// WindowID is an opaque identifier scoped to the Session that created it.
type WindowID struct {
	session *sessionCore
	native  string
}

// String returns the native identifier represented by id.
func (id WindowID) String() string {
	return id.native
}

// windowState binds a domain target to the original session for cross-capability
// operations. The target owns its own cache; no public wrapper is retained.
type windowState struct {
	id WindowID
	*sessionwindow.Target
}

func (w *windowState) ID() WindowID {
	if w == nil {
		return WindowID{}
	}
	return w.id
}
func (w *windowState) Title() string                                 { return w.target().Title() }
func (w *windowState) CachedInfo() window.Info                       { return w.target().CachedInfo() }
func (w *windowState) Info(ctx context.Context) (window.Info, error) { return w.target().Info(ctx) }
func (w *windowState) Activate(ctx context.Context) error            { return w.target().Activate(ctx) }
func (w *windowState) Move(ctx context.Context, x, y int) error      { return w.target().Move(ctx, x, y) }
func (w *windowState) Resize(ctx context.Context, width, height int) error {
	return w.target().Resize(ctx, width, height)
}
func (w *windowState) Close(ctx context.Context) error        { return w.target().Close(ctx) }
func (w *windowState) Minimize(ctx context.Context) error     { return w.target().Minimize(ctx) }
func (w *windowState) Maximize(ctx context.Context) error     { return w.target().Maximize(ctx) }
func (w *windowState) Fullscreen(ctx context.Context) error   { return w.target().Fullscreen(ctx) }
func (w *windowState) Unfullscreen(ctx context.Context) error { return w.target().Unfullscreen(ctx) }
func (w *windowState) Restore(ctx context.Context) error      { return w.target().Restore(ctx) }

func (w *windowState) target() *sessionwindow.Target {
	if w == nil {
		return nil
	}
	return w.Target
}
func (b *windowBundleState) List(ctx context.Context, match window.Match) ([]*windowState, error) {
	if b == nil {
		return nil, ErrNilSession
	}
	targets, err := b.owner.Discover(ctx, match)
	if err != nil {
		return nil, err
	}
	result := make([]*windowState, 0, len(targets))
	for _, target := range targets {
		result = append(result, &windowState{id: WindowID{session: b.session, native: target.Native()}, Target: target})
	}
	return result, nil
}
func (b *windowBundleState) window(info window.Info) *windowState {
	target := b.owner.Target(info)
	return &windowState{id: WindowID{session: b.session, native: target.Native()}, Target: target}
}

// WaitClosed waits until the authoritative backend no longer reports w.
func (w *windowState) WaitClosed(ctx context.Context) error {
	if w == nil || w.id.session == nil {
		return ErrNilSession
	}
	return w.id.session.Wait(ctx, windowClosed(w))
}

// Find returns exactly one matching window.
func (b *windowBundleState) Find(
	ctx context.Context,
	match window.Match,
) (*windowState, error) {
	windows, err := b.List(ctx, match)
	if err != nil {
		return nil, err
	}
	switch len(windows) {
	case 0:
		return nil, b.operationError("discover", fmt.Errorf("%s: %w", match.String(), ErrWindowNotFound))
	case 1:
		return windows[0], nil
	default:
		return nil, b.operationError(
			"discover",
			fmt.Errorf(
				"%s matched %d windows: %w",
				match.String(),
				len(windows),
				ErrWindowAmbiguous,
			),
		)
	}
}

// Wait waits for match to identify exactly one window.
func (b *windowBundleState) Wait(
	ctx context.Context,
	match window.Match,
	options ...WaitOption,
) (*windowState, error) {
	if b == nil || b.session == nil {
		return nil, ErrNilSession
	}
	if err := validateWindowMatch(match); err != nil {
		return nil, err
	}
	var matched *windowState
	condition := sessionCondition(
		"window "+match.String(),
		func(ctx context.Context, _ *sessionCore) (bool, error) {
			found, err := b.Find(ctx, match)
			switch {
			case err == nil:
				matched = found
				return true, nil
			case errors.Is(err, ErrWindowNotFound):
				return false, nil
			default:
				return false, err
			}
		},
		CapabilityWindows,
	)
	if err := b.session.Wait(ctx, condition, options...); err != nil {
		return nil, err
	}
	return matched, nil
}

// WaitForWindow waits for a window belonging to the application's process
// group. When the backend has no PID data, match must identify one window.
func (a *applicationState) WaitForWindow(
	ctx context.Context,
	match window.Match,
) (*windowState, error) {
	if a == nil || a.session == nil {
		return nil, ErrNilSession
	}
	if err := validateWindowMatch(match); err != nil {
		return nil, err
	}
	var matched *windowState
	condition := sessionCondition(
		"application window "+match.String(),
		func(ctx context.Context, _ *sessionCore) (bool, error) {
			select {
			case <-a.done:
				return false, ErrApplicationExited
			default:
			}
			candidates, err := a.session.windows.List(ctx, match)
			if err != nil {
				return false, err
			}
			owned := make([]*windowState, 0, len(candidates))
			hasPID := false
			for _, candidate := range candidates {
				pid := candidate.CachedInfo().PID
				if pid > 0 {
					hasPID = true
					if a.ownsPID(pid) {
						owned = append(owned, candidate)
					}
				}
			}
			if hasPID {
				candidates = owned
			}
			switch len(candidates) {
			case 0:
				return false, nil
			case 1:
				matched = candidates[0]
				return true, nil
			default:
				return false, a.session.windows.operationError(
					"discover",
					fmt.Errorf(
						"%s matched %d application windows: %w",
						match.String(),
						len(candidates),
						ErrWindowAmbiguous,
					),
				)
			}
		},
		CapabilityWindows,
	)
	if err := a.session.Wait(ctx, condition); err != nil {
		return nil, err
	}
	return matched, nil
}

// Window is a detached, non-owning handle to a stable session-bound target.
type Window struct {
	state *windowState
	scope operationScope
}

func (w *Window) privateState() *windowState {
	if w == nil {
		return nil
	}
	return w.state
}

func wrapWindow(w *windowState, scope operationScope) *Window {
	if w == nil {
		return nil
	}
	return &Window{state: w, scope: scope}
}

// ID returns the opaque session-scoped identifier.
func (w *Window) ID() WindowID {
	return w.privateState().ID()
}

// Title returns the latest title observed through Info or discovery.
func (w *Window) Title() string {
	return w.privateState().Title()
}

// CachedInfo returns the last authoritative window metadata used to create or
// refresh this handle. It does not query the window manager. Callers that need
// current geometry or focus state should use Info; callers crossing into
// another semantic subsystem during a compositor stall can use this bounded
// snapshot without issuing a second discovery request.
func (w *Window) CachedInfo() window.Info {
	return w.privateState().CachedInfo()
}

// List returns every window satisfying match.
func (s WindowBundle) listContext(
	ctx context.Context,
	match window.Match,
) ([]*Window, error) {
	values, err := s.state.List(ctx, match)
	if values == nil {
		return nil, err
	}
	result := make([]*Window, len(values))
	for i, v := range values {
		result[i] = wrapWindow(v, s.scope)
	}
	return result, err
}

// Find returns exactly one matching window.
func (s WindowBundle) findContext(
	ctx context.Context,
	match window.Match,
) (*Window, error) {
	v, err := s.state.Find(ctx, match)
	return wrapWindow(v, s.scope), err
}

// Wait waits for match to identify exactly one window.
func (s WindowBundle) waitContext(
	ctx context.Context,
	match window.Match,
	options ...WaitOption,
) (*Window, error) {
	v, err := s.state.Wait(ctx, match, options...)
	return wrapWindow(v, s.scope), err
}

func validateWindowMatch(match window.Match) error {
	if err := match.Validate(); err != nil {
		return errors.Join(ErrInvalidArgument, err)
	}
	return nil
}

// WaitActive waits until this window is observed active. It does not activate
// the window or promise that focus remains held after the observation.
func (w *Window) WaitActive(options ...WaitOption) (err error) {
	if w == nil || w.state == nil || w.state.id.session == nil {
		return ErrNilSession
	}
	ctx, finish, err := w.operationScope().begin(scopeShort, 0)
	if err != nil {
		return err
	}
	defer finish(&err)
	config := waitConfig{interval: w.state.id.session.Timeouts().Poll}
	for _, option := range options {
		if option != nil {
			if err := option(&config); err != nil {
				return err
			}
		}
	}
	return w.state.waitActive(ctx, config.interval)
}

func (w *windowState) waitActive(ctx context.Context, poll time.Duration) error {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := w.Info(ctx)
		if err != nil {
			return err
		}
		if info.Active {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
