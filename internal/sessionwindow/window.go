package sessionwindow

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"github.com/nskaggs/perfuncted/v2/window"
	"sync"
)

// WindowBundle is the session-bound window discovery and control facade.
type ownerState struct {
	backend window.Manager
	gate    capability.BoundedAuthority
}

func (b *ownerState) checkAvailable(operation string) error {
	if b == nil {
		return capability.ErrNilSession
	}
	if b.gate == nil {
		return capability.ErrNilSession
	}
	return b.gate.CheckBackend(operation, b.backend)
}

func (b *ownerState) close() error {
	if b == nil {
		return nil
	}
	if util.IsNil(b.backend) {
		return nil
	}
	return b.backend.Close()
}

// Sync refreshes pending window-backend state when supported.
func (b *ownerState) Sync(ctx context.Context) error {
	if err := b.checkAvailable("sync"); err != nil {
		return err
	}
	ctx, cancel := b.gate.BackendContext(ctx)
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
		if openErr := b.gate.EnsureOpen(); openErr != nil {
			return openErr
		}
		return b.gate.OperationError("sync", err)
	}
	return b.gate.OperationError("sync", capability.ErrUnsupported)
}

// ActiveTitle returns the title of the focused window.
func (b *ownerState) ActiveTitle(ctx context.Context) (string, error) {
	if err := b.checkAvailable("active-title"); err != nil {
		return "", err
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	title, err := b.backend.ActiveTitle(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if openErr := b.gate.EnsureOpen(); openErr != nil {
		return "", openErr
	}
	return title, b.gate.OperationError("active-title", err)
}

// Events returns the bounded, lossy stream of window lifecycle and focus
// notifications when the selected backend provides one. Events may be
// dropped, including lifecycle and focus events, if the consumer is slow.
// Refresh authoritative state with List or Window.Info after receiving an
// event. The channel closes when the session closes or the backend stops.
// Event subscription setup is performed during backend initialization under
// the session startup policy; the returned stream is caller-owned and has no
// artificial expiry.
func (b *ownerState) Events() (<-chan window.Event, error) {
	if err := b.checkAvailable("events"); err != nil {
		return nil, err
	}
	source, ok := b.backend.(window.EventSource)
	if !ok {
		return nil, b.gate.OperationError("events", window.ErrNotSupported)
	}
	return source.WindowEvents(), nil
}

// ListDesktops lists compositor-owned virtual desktops when supported.
// On KDE it returns stable desktop IDs that can be used for switching and
// assigning windows. Other backends fail explicitly rather than inventing IDs.
func (b *ownerState) ListDesktops(ctx context.Context) ([]window.DesktopInfo, error) {
	if err := b.checkAvailable("list-desktops"); err != nil {
		return nil, err
	}
	backend, ok := b.backend.(interface {
		ListDesktops(context.Context) ([]window.DesktopInfo, error)
	})
	if !ok {
		return nil, b.gate.OperationError("list-desktops", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	desktops, err := backend.ListDesktops(ctx)
	if err != nil {
		wrapped := b.gate.OperationError("list-desktops", err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errors.Join(ctxErr, wrapped)
		}
		return nil, wrapped
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return nil, err
	}
	return desktops, nil
}

// SwitchDesktop selects one existing compositor-native virtual desktop.
func (b *ownerState) SwitchDesktop(ctx context.Context, id string) error {
	if err := b.checkAvailable("switch-desktop"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SwitchDesktop(context.Context, string) error
	})
	if !ok {
		return b.gate.OperationError("switch-desktop", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "switch-desktop", backend.SwitchDesktop(ctx, id))
}

// SetWindowDesktopByID moves a window to one existing virtual desktop.
func (b *ownerState) SetWindowDesktopByID(ctx context.Context, id, desktopID string) error {
	if err := b.checkAvailable("set-window-desktop"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SetWindowDesktopByID(context.Context, string, string) error
	})
	if !ok {
		return b.gate.OperationError("set-window-desktop", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "set-window-desktop", backend.SetWindowDesktopByID(ctx, id, desktopID))
}

// SetKeepAboveByID changes KWin's compositor-owned stacking state.
func (b *ownerState) SetKeepAboveByID(ctx context.Context, id string, on bool) error {
	if err := b.checkAvailable("keep-above"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SetKeepAboveByID(context.Context, string, bool) error
	})
	if !ok {
		return b.gate.OperationError("keep-above", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "keep-above", backend.SetKeepAboveByID(ctx, id, on))
}

// SetKeepBelowByID changes KWin's compositor-owned stacking state.
func (b *ownerState) SetKeepBelowByID(ctx context.Context, id string, on bool) error {
	if err := b.checkAvailable("keep-below"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SetKeepBelowByID(context.Context, string, bool) error
	})
	if !ok {
		return b.gate.OperationError("keep-below", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "keep-below", backend.SetKeepBelowByID(ctx, id, on))
}

// CursorPosition reads the compositor's actual pointer position. This is
// separate from input injection: uinput and EIS cannot query cursor location.
func (b *ownerState) CursorPosition(ctx context.Context) (int, int, error) {
	if err := b.checkAvailable("cursor-position"); err != nil {
		return 0, 0, err
	}
	backend, ok := b.backend.(interface {
		CursorPosition(context.Context) (int, int, error)
	})
	if !ok {
		return 0, 0, b.gate.OperationError("cursor-position", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	x, y, err := backend.CursorPosition(ctx)
	if err != nil {
		wrapped := b.gate.OperationError("cursor-position", err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, 0, errors.Join(ctxErr, wrapped)
		}
		return 0, 0, wrapped
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if err := b.gate.EnsureOpen(); err != nil {
		return 0, 0, err
	}
	return x, y, nil
}

// SetShadeByID changes true KWin titlebar shading only where supported.
// It never emulates the feature with minimization or window opacity.
func (b *ownerState) SetShadeByID(ctx context.Context, id string, on bool) error {
	if err := b.checkAvailable("shade-when-supported"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		SetShadeByID(context.Context, string, bool) error
	})
	if !ok {
		return b.gate.OperationError("shade-when-supported", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "shade-when-supported", backend.SetShadeByID(ctx, id, on))
}

// TileWindowByID assigns the native KWin custom tile containing an absolute
// compositor-logical position. Backends without native tiling fail explicitly.
func (b *ownerState) TileWindowByID(ctx context.Context, id string, x, y int) error {
	if err := b.checkAvailable("tile-window"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		TileWindowByID(context.Context, string, int, int) error
	})
	if !ok {
		return b.gate.OperationError("tile-window", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "tile-window", backend.TileWindowByID(ctx, id, x, y))
}

// FloatWindowByID removes a window from its native KWin tile.
func (b *ownerState) FloatWindowByID(ctx context.Context, id string) error {
	if err := b.checkAvailable("float-window"); err != nil {
		return err
	}
	backend, ok := b.backend.(interface {
		FloatWindowByID(context.Context, string) error
	})
	if !ok {
		return b.gate.OperationError("float-window", window.ErrNotSupported)
	}
	ctx, cancel := b.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.desktopMutationResult(ctx, "float-window", backend.FloatWindowByID(ctx, id))
}

// desktopMutationResult does not acknowledge a window or desktop mutation after
// its caller has withdrawn authorization or its owning session has closed.
// Preserve concurrent backend errors for diagnosis rather than hiding them.
func (b *ownerState) desktopMutationResult(ctx context.Context, operation string, backendErr error) error {
	result := b.gate.OperationError(operation, backendErr)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return errors.Join(ctxErr, result)
	}
	if sessionErr := b.gate.EnsureOpen(); sessionErr != nil {
		return errors.Join(sessionErr, result)
	}
	return result
}

// Title returns the latest title observed through Info or discovery.
func (w *Target) Title() string {
	if w == nil {
		return ""
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshot.Title
}

// CachedInfo returns the last authoritative window metadata used to create or
// refresh this handle. It does not query the window manager. Callers that need
// current geometry or focus state should use Info; callers crossing into
// another semantic subsystem during a compositor stall can use this bounded
// snapshot without issuing a second discovery request.
func (w *Target) CachedInfo() window.Info {
	if w == nil {
		return window.Info{}
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.snapshot
}

// Info refreshes and returns authoritative window state.
func (w *Target) Info(ctx context.Context) (window.Info, error) {
	backend, err := w.backend("info")
	if err != nil {
		return window.Info{}, err
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return window.Info{}, ctxErr
	}
	info, err := backend.InfoByID(ctx, w.native)
	// A backend may acknowledge a read after caller cancellation. Do not
	// expose private window data or mutate the cached handle in that case.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return window.Info{}, ctxErr
	}
	if sessionErr := w.owner.gate.EnsureOpen(); sessionErr != nil {
		return window.Info{}, sessionErr
	}
	if err != nil {
		return window.Info{}, w.owner.gate.OperationError("info", err)
	}
	info.NativeID = w.native
	w.mu.Lock()
	w.snapshot = info
	w.mu.Unlock()
	return info, nil
}

func (w *Target) backend(operation string) (window.IDManager, error) {
	if w == nil || w.owner == nil {
		return nil, capability.ErrNilSession
	}
	if err := w.owner.checkAvailable(operation); err != nil {
		return nil, err
	}
	backend, ok := w.owner.backend.(window.IDManager)
	if !ok {
		return nil, w.owner.gate.OperationError(
			operation,
			fmt.Errorf(
				"perfuncted: window backend does not support stable handles: %w",
				window.ErrNotSupported,
			),
		)
	}
	return backend, nil
}

// Activate focuses the window.
func (w *Target) Activate(ctx context.Context) error {
	backend, err := w.backend("activate")
	if err != nil {
		return err
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("activate", backend.ActivateByID(ctx, w.native))
}

// Move positions the window at x and y. Negative coordinates are valid for a
// monitor left of or above the primary one; a coordinate no backend can carry
// is rejected with ErrInvalidArgument.
func (w *Target) Move(ctx context.Context, x int, y int) error {
	backend, err := w.backend("move")
	if err != nil {
		return err
	}
	if err := window.ValidatePosition(x, y); err != nil {
		return w.owner.gate.OperationError("move", err)
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("move", backend.MoveByID(ctx, w.native, x, y))
}

// Resize changes the window size to width by height. Both edges must be
// positive and within the geometry range every backend can represent; anything
// else is rejected with ErrInvalidArgument rather than sent to a compositor that
// would truncate it.
func (w *Target) Resize(
	ctx context.Context,
	width int,
	height int,
) error {
	backend, err := w.backend("resize")
	if err != nil {
		return err
	}
	if err := window.ValidateSize(width, height); err != nil {
		return w.owner.gate.OperationError("resize", err)
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("resize", backend.ResizeByID(ctx, w.native, width, height))
}

// Close requests that the window close.
func (w *Target) Close(ctx context.Context) error {
	backend, err := w.backend("close")
	if err != nil {
		return err
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("close", backend.CloseWindowByID(ctx, w.native))
}

// Minimize minimizes the window.
func (w *Target) Minimize(ctx context.Context) error {
	backend, err := w.backend("minimize")
	if err != nil {
		return err
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("minimize", backend.MinimizeByID(ctx, w.native))
}

// Maximize maximizes the window.
func (w *Target) Maximize(ctx context.Context) error {
	backend, err := w.backend("maximize")
	if err != nil {
		return err
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("maximize", backend.MaximizeByID(ctx, w.native))
}

// Fullscreen makes the window occupy the screen.
func (w *Target) Fullscreen(ctx context.Context) error {
	backend, err := w.backend("fullscreen")
	if err != nil {
		return err
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("fullscreen", backend.FullscreenByID(ctx, w.native))
}

// Unfullscreen restores the window from fullscreen mode.
func (w *Target) Unfullscreen(ctx context.Context) error {
	backend, err := w.backend("fullscreen")
	if err != nil {
		return err
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("fullscreen", backend.UnfullscreenByID(ctx, w.native))
}

// Restore returns the window to its normal state.
func (w *Target) Restore(ctx context.Context) error {
	backend, err := w.backend("restore")
	if err != nil {
		return err
	}
	ctx, cancel := w.owner.gate.BackendContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.owner.gate.OperationError("restore", backend.RestoreByID(ctx, w.native))
}

type Owner struct{ *ownerState }
type Handle struct{ *ownerState }

func New(gate capability.BoundedAuthority, backend window.Manager) *Owner {
	return &Owner{&ownerState{gate: gate, backend: backend}}
}
func (o *Owner) Handle() Handle {
	if o == nil {
		return Handle{}
	}
	return Handle{o.ownerState}
}
func (o *Owner) Install(backend window.Manager) { o.backend = backend }
func (o *Owner) Backend() window.Manager {
	if o == nil {
		return nil
	}
	return o.backend
}
func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	return o.close()
}
func (o *Owner) CheckAvailable(op string) error {
	if o == nil {
		return capability.ErrNilSession
	}
	return o.checkAvailable(op)
}

// Target owns its cache and immutable native binding; root retains the
// session-identity proof used when coordinating other capabilities.
type Target struct {
	owner    *ownerState
	native   string
	mu       sync.RWMutex
	snapshot window.Info
}

func (o *Owner) Target(info window.Info) *Target {
	native := info.StableID()
	info.NativeID = native
	return &Target{owner: o.ownerState, native: native, snapshot: info}
}

func (o *Owner) Discover(ctx context.Context, match window.Match) ([]*Target, error) {
	if err := o.CheckAvailable("discover"); err != nil {
		return nil, err
	}
	ctx, cancel := o.gate.BackendContext(ctx)
	defer cancel()
	infos, err := o.backend.List(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if sessionErr := o.gate.EnsureOpen(); sessionErr != nil {
		return nil, sessionErr
	}
	if err != nil {
		return nil, o.gate.OperationError("discover", err)
	}
	targets := make([]*Target, 0, len(infos))
	for _, info := range infos {
		if match.Matches(info) {
			targets = append(targets, o.Target(info))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := o.gate.EnsureOpen(); err != nil {
		return nil, err
	}
	return targets, nil
}
func (w *Target) Native() string {
	if w == nil {
		return ""
	}
	return w.native
}
