package perfuncted

import (
	"context"
	"errors"
	"fmt"
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

// WindowMatch describes a window query.
type WindowMatch = window.Match

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
func (b *windowBundleState) List(ctx context.Context, match WindowMatch) ([]*windowState, error) {
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
	match WindowMatch,
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
	match WindowMatch,
	options ...WaitOption,
) (*windowState, error) {
	if b == nil || b.session == nil {
		return nil, ErrNilSession
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
	match WindowMatch,
) (*windowState, error) {
	if a == nil || a.session == nil {
		return nil, ErrNilSession
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
