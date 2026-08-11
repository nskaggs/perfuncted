package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/nskaggs/perfuncted/window"
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
	session *Session
	native  string
}

func (id WindowID) String() string {
	return id.native
}

// Window is a stable, session-bound handle to one native window.
type Window struct {
	id WindowID

	mu       sync.RWMutex
	snapshot window.Info
}

// ID returns the opaque session-scoped identifier.
func (w *Window) ID() WindowID {
	if w == nil {
		return WindowID{}
	}
	return w.id
}

// Title returns the latest title observed through Info or discovery.
func (w *Window) Title() string {
	if w == nil {
		return ""
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshot.Title
}

// Info refreshes and returns authoritative window state.
func (w *Window) Info(ctx context.Context) (window.Info, error) {
	backend, err := w.backend("info")
	if err != nil {
		return window.Info{}, err
	}
	info, err := backend.InfoByID(ctx, w.id.native)
	if err != nil {
		return window.Info{}, w.id.session.Windows.operationError("info", err)
	}
	info.NativeID = w.id.native
	w.mu.Lock()
	w.snapshot = info
	w.mu.Unlock()
	return info, nil
}

func (w *Window) backend(operation string) (window.IDManager, error) {
	if w == nil || w.id.session == nil {
		return nil, ErrNilSession
	}
	if err := w.id.session.Windows.checkAvailable(operation); err != nil {
		return nil, err
	}
	backend, ok := w.id.session.Windows.backend.(window.IDManager)
	if !ok {
		return nil, fmt.Errorf(
			"perfuncted: window backend does not support stable handles: %w",
			window.ErrNotSupported,
		)
	}
	return backend, nil
}

func (w *Window) Activate(ctx context.Context) error {
	backend, err := w.backend("activate")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("activate", backend.ActivateByID(ctx, w.id.native))
}

func (w *Window) Move(ctx context.Context, x int, y int) error {
	backend, err := w.backend("move")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("move", backend.MoveByID(ctx, w.id.native, x, y))
}

func (w *Window) Resize(
	ctx context.Context,
	width int,
	height int,
) error {
	backend, err := w.backend("resize")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("resize", backend.ResizeByID(ctx, w.id.native, width, height))
}

// Close requests that the window close.
func (w *Window) Close(ctx context.Context) error {
	backend, err := w.backend("close")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("close", backend.CloseWindowByID(ctx, w.id.native))
}

func (w *Window) Minimize(ctx context.Context) error {
	backend, err := w.backend("minimize")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("minimize", backend.MinimizeByID(ctx, w.id.native))
}

func (w *Window) Maximize(ctx context.Context) error {
	backend, err := w.backend("maximize")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("maximize", backend.MaximizeByID(ctx, w.id.native))
}

func (w *Window) Fullscreen(ctx context.Context) error {
	backend, err := w.backend("fullscreen")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("fullscreen", backend.FullscreenByID(ctx, w.id.native))
}

func (w *Window) Unfullscreen(ctx context.Context) error {
	backend, err := w.backend("fullscreen")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("fullscreen", backend.UnfullscreenByID(ctx, w.id.native))
}

func (w *Window) Restore(ctx context.Context) error {
	backend, err := w.backend("restore")
	if err != nil {
		return err
	}
	return w.id.session.Windows.operationError("restore", backend.RestoreByID(ctx, w.id.native))
}

// WaitClosed waits until the authoritative backend no longer reports w.
func (w *Window) WaitClosed(ctx context.Context) error {
	if w == nil || w.id.session == nil {
		return ErrNilSession
	}
	return w.id.session.Wait(ctx, WindowClosed(w))
}

// List returns every window satisfying match.
func (b *WindowBundle) List(
	ctx context.Context,
	match WindowMatch,
) ([]*Window, error) {
	if err := b.checkAvailable("discover"); err != nil {
		return nil, err
	}
	infos, err := b.backend.List(ctx)
	if err != nil {
		return nil, b.operationError("discover", err)
	}
	windows := make([]*Window, 0, len(infos))
	for _, info := range infos {
		if !match.Matches(info) {
			continue
		}
		windows = append(windows, b.window(info))
	}
	return windows, nil
}

func (b *WindowBundle) window(info window.Info) *Window {
	nativeID := info.StableID()
	info.NativeID = nativeID
	id := WindowID{
		session: b.session,
		native:  nativeID,
	}
	return &Window{
		id:       id,
		snapshot: info,
	}
}

// Find returns exactly one matching window.
func (b *WindowBundle) Find(
	ctx context.Context,
	match WindowMatch,
) (*Window, error) {
	windows, err := b.List(ctx, match)
	if err != nil {
		return nil, err
	}
	switch len(windows) {
	case 0:
		return nil, fmt.Errorf("%s: %w", match.String(), ErrWindowNotFound)
	case 1:
		return windows[0], nil
	default:
		return nil, fmt.Errorf(
			"%s matched %d windows: %w",
			match.String(),
			len(windows),
			ErrWindowAmbiguous,
		)
	}
}

// Wait waits for match to identify exactly one window.
func (b *WindowBundle) Wait(
	ctx context.Context,
	match WindowMatch,
	options ...WaitOption,
) (*Window, error) {
	if b == nil || b.session == nil {
		return nil, ErrNilSession
	}
	var matched *Window
	condition := sessionCondition(
		"window "+match.String(),
		func(ctx context.Context, _ *Session) (bool, error) {
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
	)
	if err := b.session.Wait(ctx, condition, options...); err != nil {
		return nil, err
	}
	return matched, nil
}

// WaitForWindow waits for a window belonging to the application's process
// group. When the backend has no PID data, match must identify one window.
func (a *Application) WaitForWindow(
	ctx context.Context,
	match WindowMatch,
) (*Window, error) {
	if a == nil || a.session == nil {
		return nil, ErrNilSession
	}
	var matched *Window
	condition := sessionCondition(
		"application window "+match.String(),
		func(ctx context.Context, _ *Session) (bool, error) {
			select {
			case <-a.done:
				return false, ErrApplicationExited
			default:
			}
			candidates, err := a.session.Windows.List(ctx, match)
			if err != nil {
				return false, err
			}
			owned := make([]*Window, 0, len(candidates))
			hasPID := false
			for _, candidate := range candidates {
				candidate.mu.RLock()
				pid := candidate.snapshot.PID
				candidate.mu.RUnlock()
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
				return false, fmt.Errorf(
					"%s matched %d application windows: %w",
					match.String(),
					len(candidates),
					ErrWindowAmbiguous,
				)
			}
		},
	)
	if err := a.session.Wait(ctx, condition); err != nil {
		return nil, err
	}
	return matched, nil
}
