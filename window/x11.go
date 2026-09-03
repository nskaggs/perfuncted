//go:build linux

package window

import (
	"context"
	"fmt"
	"iter"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/x11"
)

var _ Manager = (*X11Backend)(nil)

// X11Backend manages windows via EWMH atoms on an X11 or XWayland display.
type X11Backend struct {
	conn                        x11.Connection
	root                        xproto.Window
	atomNetClientList           xproto.Atom
	atomNetActiveWindow         xproto.Atom
	atomNetFrameExtent          xproto.Atom
	atomNetWMName               xproto.Atom
	atomNetWMState              xproto.Atom
	atomNetWMStateHidden        xproto.Atom
	atomNetWMStateMaximizedVert xproto.Atom
	atomNetWMStateMaximizedHorz xproto.Atom
	atomNetWMStateFullscreen    xproto.Atom
	atomNetWMPID                xproto.Atom
	atomMotifWMHints            xproto.Atom
	atomUTF8String              xproto.Atom

	// lifecycleMu protects closed, active, and activeDone. It is never held
	// while an X11 operation or connection close runs.
	lifecycleMu sync.Mutex
	closed      bool
	active      int
	activeDone  chan struct{}
	closeOnce   sync.Once
}

// NewX11Backend connects to the X11 display and interns the EWMH atoms needed
// for window management. Pass an empty string to use the DISPLAY environment variable.
func NewX11Backend(displayName string) (*X11Backend, error) {
	conn, err := x11.NewXgbConnection(displayName)
	if err != nil {
		return nil, fmt.Errorf("window/x11: connect to display %q: %w", displayName, err)
	}
	b := &X11Backend{conn: conn}
	b.root = conn.DefaultScreen().Root

	atoms := map[string]*xproto.Atom{
		"_NET_CLIENT_LIST":             &b.atomNetClientList,
		"_NET_ACTIVE_WINDOW":           &b.atomNetActiveWindow,
		"_NET_FRAME_EXTENTS":           &b.atomNetFrameExtent,
		"_NET_WM_NAME":                 &b.atomNetWMName,
		"_NET_WM_STATE":                &b.atomNetWMState,
		"_NET_WM_STATE_HIDDEN":         &b.atomNetWMStateHidden,
		"_NET_WM_STATE_MAXIMIZED_VERT": &b.atomNetWMStateMaximizedVert,
		"_NET_WM_STATE_MAXIMIZED_HORZ": &b.atomNetWMStateMaximizedHorz,
		"_NET_WM_STATE_FULLSCREEN":     &b.atomNetWMStateFullscreen,
		"_NET_WM_PID":                  &b.atomNetWMPID,
		"_MOTIF_WM_HINTS":              &b.atomMotifWMHints,
		"UTF8_STRING":                  &b.atomUTF8String,
	}
	for name, ptr := range atoms {
		rep, err := b.conn.InternAtom(false, uint16(len(name)), name).Reply()
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("window/x11: intern atom %q: %w", name, err)
		}
		*ptr = rep.Atom
	}
	return b, nil
}

func (b *X11Backend) activeWindow() (xproto.Window, error) {
	rep, err := b.conn.GetProperty(false, b.root, b.atomNetActiveWindow,
		xproto.AtomWindow, 0, 1).Reply()
	if err != nil {
		return 0, err
	}
	if len(rep.Value) < 4 {
		return 0, nil
	}
	id := xproto.Window(
		uint32(rep.Value[0]) | uint32(rep.Value[1])<<8 |
			uint32(rep.Value[2])<<16 | uint32(rep.Value[3])<<24)
	return id, nil
}

func (b *X11Backend) windowState(win xproto.Window) (minimized, maximized, fullscreen bool) {
	rep, err := b.conn.GetProperty(false, win, b.atomNetWMState,
		xproto.AtomAtom, 0, 64).Reply()
	if err != nil || rep.Format != 32 {
		return
	}
	var maximizedVert, maximizedHorz bool
	for i := 0; i+3 < len(rep.Value); i += 4 {
		a := xproto.Atom(uint32(rep.Value[i]) | uint32(rep.Value[i+1])<<8 |
			uint32(rep.Value[i+2])<<16 | uint32(rep.Value[i+3])<<24)
		switch a {
		case b.atomNetWMStateHidden:
			minimized = true
		case b.atomNetWMStateMaximizedVert:
			maximizedVert = true
		case b.atomNetWMStateMaximizedHorz:
			maximizedHorz = true
		case b.atomNetWMStateFullscreen:
			fullscreen = true
		}
	}
	maximized = maximizedVert && maximizedHorz
	return
}

// windowTitle returns the title of a window, trying _NET_WM_NAME then WM_NAME.
func (b *X11Backend) windowTitle(win xproto.Window) string {
	// Try _NET_WM_NAME (UTF-8) first.
	rep, err := b.conn.GetProperty(false, win, b.atomNetWMName,
		b.atomUTF8String, 0, 1024).Reply()
	if err == nil && len(rep.Value) > 0 {
		return string(rep.Value)
	}
	// Fallback: WM_NAME (Latin-1).
	rep, err = b.conn.GetProperty(false, win, xproto.AtomWmName,
		xproto.AtomString, 0, 1024).Reply()
	if err == nil && len(rep.Value) > 0 {
		return string(rep.Value)
	}
	return ""
}

// windowPID returns the PID stored in _NET_WM_PID, or 0 if unavailable.
func (b *X11Backend) windowPID(win xproto.Window) int32 {
	rep, err := b.conn.GetProperty(false, win, b.atomNetWMPID,
		b.atomNetWMPID, 0, 1).Reply()
	if err != nil || len(rep.Value) < 4 {
		return 0
	}
	return int32(uint32(rep.Value[0]) | uint32(rep.Value[1])<<8 |
		uint32(rep.Value[2])<<16 | uint32(rep.Value[3])<<24)
}

// windowClass returns the WM_CLASS instance and class strings, if available.
func (b *X11Backend) windowClass(win xproto.Window) (string, string) {
	rep, err := b.conn.GetProperty(false, win, xproto.AtomWmClass,
		xproto.AtomString, 0, 1024).Reply()
	if err != nil || len(rep.Value) == 0 {
		return "", ""
	}
	parts := strings.Split(string(rep.Value), "\x00")
	if len(parts) == 0 {
		return "", ""
	}
	appID := strings.TrimSpace(parts[0])
	class := appID
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		class = strings.TrimSpace(parts[1])
	}
	return appID, class
}

// windowHasDecoration checks if the window declared it has decoration at the WM level. Some windows are still
// using the old Motif way of doing things. On modern desktop, in conjunction with using _NET_FRAME_EXTENTS correctly,
// this "trick" allows them to bypass the decoration settings which is done by WM ; the WM allocates the decoration
// space (because of _NET_FRAME_EXTENTS) but does not draw them, leaving this task to the application itself.
func (b *X11Backend) windowHasDecoration(win xproto.Window) bool {
	rep, err := b.conn.GetProperty(false, win, b.atomMotifWMHints,
		b.atomMotifWMHints, 0, 5).Reply()
	if err != nil || len(rep.Value) < 20 {
		return true
	}

	flags := uint32(rep.Value[0]) | uint32(rep.Value[1])<<8 | uint32(rep.Value[2])<<16 | uint32(rep.Value[3])<<24
	decorations := uint32(rep.Value[8]) | uint32(rep.Value[9])<<8 | uint32(rep.Value[10])<<16 | uint32(rep.Value[11])<<24

	const MwmHintsDecorations = 1 << 1

	// If the app didn't set MwmHintsDecorations, default is to have decorations.
	// If it did set the flag, use the decorations value (0 = no decorations).
	if (flags & MwmHintsDecorations) == 0 {
		return true
	}
	return decorations != 0
}

// windowGeometry returns the geometry of a window, including its decoration
func (b *X11Backend) windowGeometry(win xproto.Window) (int, int, int, int) {
	geo, err := b.conn.GetGeometry(xproto.Drawable(win)).Reply()
	if err != nil {
		return 0, 0, 0, 0
	}

	trans, err := b.conn.TranslateCoordinates(win, b.root, 0, 0).Reply()
	if err != nil {
		return int(geo.X), int(geo.Y), int(geo.Width), int(geo.Height)
	}

	if !b.windowHasDecoration(win) {
		return int(trans.DstX), int(trans.DstY), int(geo.Width), int(geo.Height)
	}

	x := int(trans.DstX)
	y := int(trans.DstY)
	w := int(geo.Width)
	h := int(geo.Height)

	// decorated windows shall be translated again by the size of the decorations
	reply, err := b.conn.GetProperty(false, win, b.atomNetFrameExtent,
		b.atomNetFrameExtent, 0, 4).Reply()
	if err == nil && len(reply.Value) == 16 {
		left := int(uint32(reply.Value[0]) | uint32(reply.Value[1])<<8 | uint32(reply.Value[2])<<16 | uint32(reply.Value[3])<<24)
		right := int(uint32(reply.Value[4]) | uint32(reply.Value[5])<<8 | uint32(reply.Value[6])<<16 | uint32(reply.Value[7])<<24)
		top := int(uint32(reply.Value[8]) | uint32(reply.Value[9])<<8 | uint32(reply.Value[10])<<16 | uint32(reply.Value[11])<<24)
		bottom := int(uint32(reply.Value[12]) | uint32(reply.Value[13])<<8 | uint32(reply.Value[14])<<16 | uint32(reply.Value[15])<<24)

		x -= left
		y -= top
		w += left + right
		h += top + bottom
	}

	return x, y, w, h
}

// List returns top-level windows from the X11 client list.
func (b *X11Backend) List(ctx context.Context) ([]Info, error) {
	var out []Info
	for win, err := range b.IterateWindows(ctx) {
		if err != nil {
			return nil, err
		}
		out = append(out, win)
	}
	return out, nil
}

// IterateWindows returns an iterator over all top-level windows.
func (b *X11Backend) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		ctx = contextutil.Default(ctx)
		if err := ctx.Err(); err != nil {
			yield(Info{}, fmt.Errorf("window/x11: iterate canceled: %w", err))
			return
		}
		finish, err := b.beginOperation()
		if err != nil {
			yield(Info{}, err)
			return
		}
		defer finish()
		rep, err := b.conn.GetProperty(false, b.root, b.atomNetClientList,
			xproto.AtomWindow, 0, 1024).Reply()
		if err != nil {
			yield(Info{}, fmt.Errorf("window/x11: get _NET_CLIENT_LIST: %w", err))
			return
		}
		// Format 0 means the property is not set (no WM or no windows yet) — treat as empty.
		if rep.Format == 0 {
			return
		}
		if rep.Format != 32 {
			yield(Info{}, fmt.Errorf("window/x11: unexpected _NET_CLIENT_LIST format %d", rep.Format))
			return
		}
		if len(rep.Value)%4 != 0 {
			yield(Info{}, fmt.Errorf("window/x11: malformed _NET_CLIENT_LIST payload length %d", len(rep.Value)))
			return
		}
		ids := make([]xproto.Window, len(rep.Value)/4)
		for i := range ids {
			ids[i] = xproto.Window(
				uint32(rep.Value[i*4]) | uint32(rep.Value[i*4+1])<<8 |
					uint32(rep.Value[i*4+2])<<16 | uint32(rep.Value[i*4+3])<<24)
		}

		// in case of error, activeWindow is not likely to match any id value
		activeWindow, _ := b.activeWindow()

		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				yield(Info{}, fmt.Errorf("window/x11: iterate canceled: %w", err))
				return
			}
			x, y, w, h := b.windowGeometry(id)
			minimized, maximized, fullscreen := b.windowState(id)
			appID, class := b.windowClass(id)
			info := Info{
				ID:         uint64(id),
				NativeID:   strconv.FormatUint(uint64(id), 10),
				Title:      b.windowTitle(id),
				AppID:      appID,
				Class:      class,
				PID:        b.windowPID(id),
				X:          x,
				Y:          y,
				W:          w,
				H:          h,
				Minimized:  minimized,
				Maximized:  maximized,
				Fullscreen: fullscreen,
				Active:     id == activeWindow,
			}
			if !yield(info, nil) {
				return
			}
		}
	}
}

func (b *X11Backend) setWMState(win xproto.Window, action uint32, atoms ...xproto.Atom) error {
	var data [5]uint32
	data[0] = action
	for i := 0; i < len(atoms) && i < 3; i++ {
		data[i+1] = uint32(atoms[i])
	}
	data[4] = 1
	return b.conn.SendEventChecked(false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   b.atomNetWMState,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check()
}

// ActiveTitle returns the title of the currently focused window.
func (b *X11Backend) ActiveTitle(ctx context.Context) (string, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("window/x11: active title canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return "", err
	}
	defer finish()
	id, err := b.activeWindow()
	if err != nil {
		return "", fmt.Errorf("window/x11: get _NET_ACTIVE_WINDOW: %w", err)
	}
	if id == 0 {
		return "", nil
	}
	return b.windowTitle(id), nil
}

// Close closes the X11 connection.
func (b *X11Backend) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.lifecycleMu.Lock()
		b.closed = true
		activeDone := b.activeDone
		conn := b.conn
		b.lifecycleMu.Unlock()

		// Close the connection before waiting so blocked X11 replies can
		// return and release their admission.
		if conn != nil {
			conn.Close()
		}
		if activeDone != nil {
			<-activeDone
		}
	})
	return nil
}

func (b *X11Backend) beginOperation() (func(), error) {
	if b == nil {
		return nil, fmt.Errorf("window/x11: backend is nil")
	}

	b.lifecycleMu.Lock()
	defer b.lifecycleMu.Unlock()
	if b.closed {
		return nil, fmt.Errorf("window/x11: backend is closed: %w", net.ErrClosed)
	}
	if b.active == 0 {
		b.activeDone = make(chan struct{})
	}
	b.active++

	return sync.OnceFunc(func() {
		b.lifecycleMu.Lock()
		b.active--
		if b.active == 0 {
			close(b.activeDone)
			b.activeDone = nil
		}
		b.lifecycleMu.Unlock()
	}), nil
}

// Sync flushes pending X11 requests.
func (b *X11Backend) Sync(ctx context.Context) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: sync canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	b.conn.Sync()
	return nil
}

// SupportedOperations returns operations supported by EWMH.
func (b *X11Backend) SupportedOperations() []string {
	return []string{
		"discover",
		"info",
		"active-title",
		"activate",
		"move",
		"resize",
		"close",
		"minimize",
		"maximize",
		"fullscreen",
		"restore",
	}
}

// --- Handle-based operations ---

func x11WindowID(id string) (xproto.Window, error) {
	numeric, err := numericID(id)
	if err != nil {
		return 0, err
	}
	return xproto.Window(numeric), nil
}

// ActivateByID focuses the window identified by id.
func (b *X11Backend) ActivateByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: activate canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	data := []uint32{1, uint32(xproto.TimeCurrentTime), 0, 0, 0}
	return b.conn.SendEventChecked(false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   b.atomNetActiveWindow,
			Data:   xproto.ClientMessageDataUnionData32New(data),
		}.Bytes())).Check()
}

// MoveByID positions the window identified by id.
func (b *X11Backend) MoveByID(ctx context.Context, id string, x, y int) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: move canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	return b.conn.ConfigureWindowChecked(win,
		xproto.ConfigWindowX|xproto.ConfigWindowY,
		[]uint32{uint32(x), uint32(y)}).Check()
}

// ResizeByID resizes the window identified by id.
func (b *X11Backend) ResizeByID(ctx context.Context, id string, w, h int) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: resize canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	return b.conn.ConfigureWindowChecked(win,
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(w), uint32(h)}).Check()
}

// CloseWindowByID closes the window identified by id.
func (b *X11Backend) CloseWindowByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: close canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	const wmDeleteWindow = "WM_DELETE_WINDOW"
	const wmProtocols = "WM_PROTOCOLS"
	delAtom, err := b.conn.InternAtom(false, uint16(len(wmDeleteWindow)), wmDeleteWindow).Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern WM_DELETE_WINDOW: %w", err)
	}
	protoAtom, err := b.conn.InternAtom(false, uint16(len(wmProtocols)), wmProtocols).Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern WM_PROTOCOLS: %w", err)
	}
	data := [5]uint32{uint32(delAtom.Atom), uint32(xproto.TimeCurrentTime), 0, 0, 0}
	return b.conn.SendEventChecked(false, win, 0,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   protoAtom.Atom,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check()
}

// MinimizeByID minimizes the window identified by id.
func (b *X11Backend) MinimizeByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: minimize canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	const wmChangeState = "WM_CHANGE_STATE"
	csAtom, err := b.conn.InternAtom(false, uint16(len(wmChangeState)), wmChangeState).Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern WM_CHANGE_STATE: %w", err)
	}
	data := [5]uint32{3, 0, 0, 0, 0}
	return b.conn.SendEventChecked(false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   csAtom.Atom,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check()
}

// MaximizeByID maximizes the window identified by id.
func (b *X11Backend) MaximizeByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: maximize canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	return b.setWMState(win, 1, b.atomNetWMStateMaximizedVert, b.atomNetWMStateMaximizedHorz)
}

// FullscreenByID enables fullscreen mode for the window identified by id.
func (b *X11Backend) FullscreenByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: fullscreen canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	return b.setWMState(win, 1, b.atomNetWMStateFullscreen)
}

// UnfullscreenByID disables fullscreen mode for the window identified by id.
func (b *X11Backend) UnfullscreenByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: unfullscreen canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	return b.setWMState(win, 0, b.atomNetWMStateFullscreen)
}

// RestoreByID restores the window identified by id.
func (b *X11Backend) RestoreByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/x11: restore canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return err
	}
	if err := b.setWMState(win, 0, b.atomNetWMStateMaximizedVert, b.atomNetWMStateMaximizedHorz); err != nil {
		return err
	}
	return b.conn.MapWindowChecked(win).Check()
}

// InfoByID returns fresh information for the window identified by id.
func (b *X11Backend) InfoByID(ctx context.Context, id string) (Info, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return Info{}, fmt.Errorf("window/x11: info canceled: %w", err)
	}
	finish, err := b.beginOperation()
	if err != nil {
		return Info{}, err
	}
	defer finish()
	win, err := x11WindowID(id)
	if err != nil {
		return Info{}, err
	}
	for info, listErr := range b.IterateWindows(ctx) {
		if listErr != nil {
			return Info{}, listErr
		}
		if info.ID == uint64(win) {
			return info, nil
		}
	}
	return Info{}, ErrWindowNotFound
}
