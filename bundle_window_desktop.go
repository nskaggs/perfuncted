package perfuncted

import (
	"context"
	"errors"

	"github.com/nskaggs/perfuncted/window"
)

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
