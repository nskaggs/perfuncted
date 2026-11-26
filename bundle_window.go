package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/window"
)

// WindowBundle is the session-bound window discovery and control facade.
type WindowBundle struct {
	backend window.Manager
	bundleBase
}

// WindowEventKind identifies a window lifecycle or focus notification.
type WindowEventKind = window.EventKind

const (
	// WindowAddedEvent reports a newly visible window.
	WindowAddedEvent = window.WindowAddedEvent
	// WindowRemovedEvent reports a window that is no longer managed.
	WindowRemovedEvent = window.WindowRemovedEvent
	// WindowChangedEvent reports changed window metadata or state.
	WindowChangedEvent = window.WindowChangedEvent
	// FocusChangedEvent reports a changed focus target.
	FocusChangedEvent = window.FocusChangedEvent
)

// WindowEvent is a window lifecycle or focus notification from a backend.
type WindowEvent = window.Event

func (b *WindowBundle) checkAvailable(operation string) error {
	if b == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return b.checkBackend(operation, b.backend)
}

func (b *WindowBundle) close() error {
	if b == nil {
		return nil
	}
	return closeBackend(b.backend)
}

// Sync refreshes pending window-backend state when supported.
func (b *WindowBundle) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	type syncer interface {
		Sync(context.Context) error
	}
	if backend, ok := b.backend.(syncer); ok {
		err := backend.Sync(ctx)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errors.Join(ctxErr, err)
		}
		if openErr := b.session.ensureOpen(); openErr != nil {
			return openErr
		}
		return b.operationError("sync", err)
	}
	return b.operationError("sync", ErrUnsupported)
}

// ActiveTitle returns the title of the focused window.
func (b *WindowBundle) ActiveTitle(ctx context.Context) (string, error) {
	if err := b.checkAvailable("active-title"); err != nil {
		return "", err
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	title, err := b.backend.ActiveTitle(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if openErr := b.session.ensureOpen(); openErr != nil {
		return "", openErr
	}
	return title, b.operationError("active-title", err)
}

// Events returns the bounded, lossy stream of window lifecycle and focus
// notifications when the selected backend provides one. Events may be
// dropped, including lifecycle and focus events, if the consumer is slow.
// Refresh authoritative state with List or Window.Info after receiving an
// event. The channel closes when the session closes or the backend stops.
// Event subscription setup is performed during backend initialization under
// the session startup policy; the returned stream is caller-owned and has no
// artificial expiry.
func (b *WindowBundle) Events() (<-chan WindowEvent, error) {
	if err := b.checkAvailable("events"); err != nil {
		return nil, err
	}
	source, ok := b.backend.(window.EventSource)
	if !ok {
		return nil, b.operationError("events", window.ErrNotSupported)
	}
	return source.WindowEvents(), nil
}

// ListDesktops lists compositor-owned virtual desktops when supported.
// On KDE it returns stable desktop IDs that can be used for switching and
// assigning windows. Other backends fail explicitly rather than inventing IDs.
func (b *WindowBundle) ListDesktops(ctx context.Context) ([]window.DesktopInfo, error) {
	if err := b.checkAvailable("list-desktops"); err != nil {
		return nil, err
	}
	backend, ok := b.backend.(interface {
		ListDesktops(context.Context) ([]window.DesktopInfo, error)
	})
	if !ok {
		return nil, b.operationError("list-desktops", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	desktops, err := backend.ListDesktops(ctx)
	if err != nil {
		wrapped := b.operationError("list-desktops", err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errors.Join(ctxErr, wrapped)
		}
		return nil, wrapped
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.session.ensureOpen(); err != nil {
		return nil, err
	}
	return desktops, nil
}

// SwitchDesktop selects one existing compositor-native virtual desktop.
func (b *WindowBundle) SwitchDesktop(ctx context.Context, id string) error {
	if err := b.checkAvailable("switch-desktop"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SwitchDesktop(context.Context, string) error
	})
	if !ok {
		return b.operationError("switch-desktop", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "switch-desktop", backend.SwitchDesktop(ctx, id))
}

// SetWindowDesktopByID moves a window to one existing virtual desktop.
func (b *WindowBundle) SetWindowDesktopByID(ctx context.Context, id, desktopID string) error {
	if err := b.checkAvailable("set-window-desktop"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SetWindowDesktopByID(context.Context, string, string) error
	})
	if !ok {
		return b.operationError("set-window-desktop", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "set-window-desktop", backend.SetWindowDesktopByID(ctx, id, desktopID))
}

// SetKeepAboveByID changes KWin's compositor-owned stacking state.
func (b *WindowBundle) SetKeepAboveByID(ctx context.Context, id string, on bool) error {
	if err := b.checkAvailable("keep-above"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SetKeepAboveByID(context.Context, string, bool) error
	})
	if !ok {
		return b.operationError("keep-above", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "keep-above", backend.SetKeepAboveByID(ctx, id, on))
}

// SetKeepBelowByID changes KWin's compositor-owned stacking state.
func (b *WindowBundle) SetKeepBelowByID(ctx context.Context, id string, on bool) error {
	if err := b.checkAvailable("keep-below"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SetKeepBelowByID(context.Context, string, bool) error
	})
	if !ok {
		return b.operationError("keep-below", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "keep-below", backend.SetKeepBelowByID(ctx, id, on))
}

// CursorPosition reads the compositor's actual pointer position. This is
// separate from input injection: uinput and EIS cannot query cursor location.
func (b *WindowBundle) CursorPosition(ctx context.Context) (int, int, error) {
	if err := b.checkAvailable("cursor-position"); err != nil {
		return 0, 0, err
	}
	backend, ok := b.backend.(interface {
		CursorPosition(context.Context) (int, int, error)
	})
	if !ok {
		return 0, 0, b.operationError("cursor-position", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	x, y, err := backend.CursorPosition(ctx)
	if err != nil {
		wrapped := b.operationError("cursor-position", err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, 0, errors.Join(ctxErr, wrapped)
		}
		return 0, 0, wrapped
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if err := b.session.ensureOpen(); err != nil {
		return 0, 0, err
	}
	return x, y, nil
}

// SetShadeByID changes true KWin titlebar shading only where supported.
// It never emulates the feature with minimization or window opacity.
func (b *WindowBundle) SetShadeByID(ctx context.Context, id string, on bool) error {
	if err := b.checkAvailable("shade-when-supported"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SetShadeByID(context.Context, string, bool) error
	})
	if !ok {
		return b.operationError("shade-when-supported", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "shade-when-supported", backend.SetShadeByID(ctx, id, on))
}

// TileWindowByID assigns the native KWin custom tile containing an absolute
// compositor-logical position. Backends without native tiling fail explicitly.
func (b *WindowBundle) TileWindowByID(ctx context.Context, id string, x, y int) error {
	if err := b.checkAvailable("tile-window"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		TileWindowByID(context.Context, string, int, int) error
	})
	if !ok {
		return b.operationError("tile-window", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "tile-window", backend.TileWindowByID(ctx, id, x, y))
}

// FloatWindowByID removes a window from its native KWin tile.
func (b *WindowBundle) FloatWindowByID(ctx context.Context, id string) error {
	if err := b.checkAvailable("float-window"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		FloatWindowByID(context.Context, string) error
	})
	if !ok {
		return b.operationError("float-window", window.ErrNotSupported)
	}
	ctx, cancel := b.backendContext(ctx, b.session.Timeouts().Medium)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "float-window", backend.FloatWindowByID(ctx, id))
}

// desktopMutationResult does not acknowledge a window or desktop mutation after
// its caller has withdrawn authorization or its owning session has closed.
// Preserve concurrent backend errors for diagnosis rather than hiding them.
func (b *WindowBundle) desktopMutationResult(ctx context.Context, operation string, backendErr error) error {
	result := b.operationError(operation, backendErr)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(ctxErr, result)
	}
	if sessionErr := b.session.ensureOpen(); sessionErr != nil {
		return errors.Join(sessionErr, result)
	}
	return result
}
