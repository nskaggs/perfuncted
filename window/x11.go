//go:build linux
// +build linux

package window

import (
	"context"
	"fmt"
	"iter"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

var _ Manager = (*X11Backend)(nil)

// X11Backend manages windows via EWMH atoms on an X11 or XWayland display.
type X11Backend struct {
	conn                x11.Connection
	root                xproto.Window
	atomNetClientList   xproto.Atom
	atomNetActiveWindow xproto.Atom
	atomNetFrameExtent  xproto.Atom
	atomNetWMName       xproto.Atom
	atomNetWMPID        xproto.Atom
	atomMotifWMHints    xproto.Atom
	atomUTF8String      xproto.Atom
}

// NewX11Backend connects to the X11 display and interns the EWMH atoms needed
// for window management. Pass an empty string to use the DISPLAY environment variable.
func NewX11Backend(displayName string) (*X11Backend, error) {
	conn, err := x11.NewXgbConnection(displayName)
	if err != nil {
		return nil, fmt.Errorf("window/x11: connect to display %q: %w", displayName, err)
	}
	b := &X11Backend{conn: conn}
	b.root = b.conn.DefaultScreen().Root

	atoms := map[string]*xproto.Atom{
		"_NET_CLIENT_LIST":   &b.atomNetClientList,
		"_NET_ACTIVE_WINDOW": &b.atomNetActiveWindow,
		"_NET_FRAME_EXTENTS": &b.atomNetFrameExtent,
		"_NET_WM_NAME":       &b.atomNetWMName,
		"_NET_WM_PID":        &b.atomNetWMPID,
		"_MOTIF_WM_HINTS":    &b.atomMotifWMHints,
		"UTF8_STRING":        &b.atomUTF8String,
	}
	for name, ptr := range atoms {
		reply, err := b.conn.InternAtom(false, uint16(len(name)), name).Reply()
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("window/x11: intern atom %q: %w", name, err)
		}
		*ptr = reply.Atom
	}
	return b, nil
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
		xproto.AtomCardinal, 0, 1).Reply()
	if err != nil || len(rep.Value) < 4 {
		return 0
	}
	return int32(uint32(rep.Value[0]) | uint32(rep.Value[1])<<8 |
		uint32(rep.Value[2])<<16 | uint32(rep.Value[3])<<24)
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
		xproto.AtomCardinal, 0, 4).Reply()
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
		rep, err := b.conn.GetProperty(false, b.root, b.atomNetClientList,
			xproto.AtomWindow, 0, 1024).Reply()
		if err != nil {
			yield(Info{}, fmt.Errorf("window/x11: get _NET_CLIENT_LIST: %w", err))
			return
		}
		if rep.Format != 32 {
			yield(Info{}, fmt.Errorf("window/x11: unexpected _NET_CLIENT_LIST format %d", rep.Format))
			return
		}
		ids := make([]xproto.Window, len(rep.Value)/4)
		for i := range ids {
			ids[i] = xproto.Window(
				uint32(rep.Value[i*4]) | uint32(rep.Value[i*4+1])<<8 |
					uint32(rep.Value[i*4+2])<<16 | uint32(rep.Value[i*4+3])<<24)
		}

		for _, id := range ids {
			x, y, w, h := b.windowGeometry(id)
			info := Info{
				ID:    uint64(id),
				Title: b.windowTitle(id),
				PID:   b.windowPID(id),
				X:     x,
				Y:     y,
				W:     w,
				H:     h,
			}
			if !yield(info, nil) {
				return
			}
		}
	}
}

// findByTitle returns the first window whose title contains the given string (case-insensitive).
func (b *X11Backend) findByTitle(ctx context.Context, title string) (xproto.Window, error) {
	info, err := FindByTitle(ctx, b, title)
	if err != nil {
		return 0, fmt.Errorf("window/x11: %w", err)
	}
	return xproto.Window(info.ID), nil
}

// Activate raises and focuses a window by title using _NET_ACTIVE_WINDOW.
func (b *X11Backend) Activate(ctx context.Context, title string) error {
	win, err := b.findByTitle(ctx, title)
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

// Restore restores the window by removing maximized states and mapping it.
func (b *X11Backend) Restore(ctx context.Context, title string) error {
	win, err := b.findByTitle(ctx, title)
	if err != nil {
		return err
	}
	stateAtom, err := b.conn.InternAtom(false, 14, "_NET_WM_STATE").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE: %w", err)
	}
	maxV, err := b.conn.InternAtom(false, 28, "_NET_WM_STATE_MAXIMIZED_VERT").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE_MAXIMIZED_VERT: %w", err)
	}
	maxH, err := b.conn.InternAtom(false, 28, "_NET_WM_STATE_MAXIMIZED_HORZ").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE_MAXIMIZED_HORZ: %w", err)
	}
	data := [5]uint32{0 /* _NET_WM_STATE_REMOVE */, uint32(maxV.Atom), uint32(maxH.Atom), 1, 0}
	if err := b.conn.SendEventChecked(false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   stateAtom.Atom,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check(); err != nil {
		return err
	}
	return b.conn.MapWindowChecked(win).Check()
}

// Move repositions a window by title.
func (b *X11Backend) Move(ctx context.Context, title string, x, y int) error {
	win, err := b.findByTitle(ctx, title)
	if err != nil {
		return err
	}
	return b.conn.ConfigureWindowChecked(win,
		xproto.ConfigWindowX|xproto.ConfigWindowY,
		[]uint32{uint32(x), uint32(y)}).Check()
}

// Resize changes window dimensions by title.
func (b *X11Backend) Resize(ctx context.Context, title string, w, h int) error {
	win, err := b.findByTitle(ctx, title)
	if err != nil {
		return err
	}
	return b.conn.ConfigureWindowChecked(win,
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(w), uint32(h)}).Check()
}

// ActiveTitle returns the title of the currently focused window.
func (b *X11Backend) ActiveTitle(ctx context.Context) (string, error) {
	rep, err := b.conn.GetProperty(false, b.root, b.atomNetActiveWindow,
		xproto.AtomWindow, 0, 1).Reply()
	if err != nil {
		return "", fmt.Errorf("window/x11: get _NET_ACTIVE_WINDOW: %w", err)
	}
	if len(rep.Value) < 4 {
		return "", nil
	}
	id := xproto.Window(uint32(rep.Value[0]) | uint32(rep.Value[1])<<8 |
		uint32(rep.Value[2])<<16 | uint32(rep.Value[3])<<24)
	return b.windowTitle(id), nil
}

// Close closes the X11 connection.
func (b *X11Backend) Close() error {
	b.conn.Close()
	return nil
}

// CloseWindow sends a WM_DELETE_WINDOW message to close the window gracefully.
func (b *X11Backend) CloseWindow(ctx context.Context, title string) error {
	win, err := b.findByTitle(ctx, title)
	if err != nil {
		return err
	}
	// Intern WM_DELETE_WINDOW and WM_PROTOCOLS atoms.
	delAtom, err := b.conn.InternAtom(false, 18, "WM_DELETE_WINDOW").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern WM_DELETE_WINDOW: %w", err)
	}
	protoAtom, err := b.conn.InternAtom(false, 12, "WM_PROTOCOLS").Reply()
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

// Minimize iconifies the window by setting _NET_WM_STATE_HIDDEN via the WM.
func (b *X11Backend) Minimize(ctx context.Context, title string) error {
	win, err := b.findByTitle(ctx, title)
	if err != nil {
		return err
	}
	// Use XIconifyWindow via ChangeProperty with WM_CHANGE_STATE.
	csAtom, err := b.conn.InternAtom(false, 15, "WM_CHANGE_STATE").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern WM_CHANGE_STATE: %w", err)
	}
	data := [5]uint32{3 /* IconicState */, 0, 0, 0, 0}
	return b.conn.SendEventChecked(false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   csAtom.Atom,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check()
}

// Maximize sets _NET_WM_STATE_MAXIMIZED_VERT and _NET_WM_STATE_MAXIMIZED_HORZ.
func (b *X11Backend) Maximize(ctx context.Context, title string) error {
	win, err := b.findByTitle(ctx, title)
	if err != nil {
		return err
	}
	stateAtom, err := b.conn.InternAtom(false, 14, "_NET_WM_STATE").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE: %w", err)
	}
	maxV, err := b.conn.InternAtom(false, 28, "_NET_WM_STATE_MAXIMIZED_VERT").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE_MAXIMIZED_VERT: %w", err)
	}
	maxH, err := b.conn.InternAtom(false, 28, "_NET_WM_STATE_MAXIMIZED_HORZ").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE_MAXIMIZED_HORZ: %w", err)
	}
	data := [5]uint32{1 /* _NET_WM_STATE_ADD */, uint32(maxV.Atom), uint32(maxH.Atom), 1 /* source = application */, 0}
	return b.conn.SendEventChecked(false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   stateAtom.Atom,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check()
}
