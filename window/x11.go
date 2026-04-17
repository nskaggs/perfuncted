//go:build linux
// +build linux

package window

import (
	"fmt"
	"strings"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// X11Backend manages windows via EWMH atoms on an X11 or XWayland display.
type X11Backend struct {
	conn                *xgb.Conn
	root                xproto.Window
	atomNetClientList   xproto.Atom
	atomNetActiveWindow xproto.Atom
	atomNetWMName       xproto.Atom
	atomNetWMPID        xproto.Atom
	atomUTF8String      xproto.Atom
}

// NewX11Backend connects to the X11 display and interns the EWMH atoms needed
// for window management. Pass an empty string to use the DISPLAY environment variable.
func NewX11Backend(displayName string) (*X11Backend, error) {
	conn, err := xgb.NewConnDisplay(displayName)
	if err != nil {
		return nil, fmt.Errorf("window/x11: connect to display %q: %w", displayName, err)
	}
	b := &X11Backend{conn: conn}
	b.root = xproto.Setup(conn).DefaultScreen(conn).Root

	atoms := map[string]*xproto.Atom{
		"_NET_CLIENT_LIST":   &b.atomNetClientList,
		"_NET_ACTIVE_WINDOW": &b.atomNetActiveWindow,
		"_NET_WM_NAME":       &b.atomNetWMName,
		"_NET_WM_PID":        &b.atomNetWMPID,
		"UTF8_STRING":        &b.atomUTF8String,
	}
	for name, ptr := range atoms {
		reply, err := xproto.InternAtom(conn, false, uint16(len(name)), name).Reply()
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
	rep, err := xproto.GetProperty(b.conn, false, win, b.atomNetWMName,
		b.atomUTF8String, 0, 1024).Reply()
	if err == nil && len(rep.Value) > 0 {
		return string(rep.Value)
	}
	// Fallback: WM_NAME (Latin-1).
	rep, err = xproto.GetProperty(b.conn, false, win, xproto.AtomWmName,
		xproto.AtomString, 0, 1024).Reply()
	if err == nil && len(rep.Value) > 0 {
		return string(rep.Value)
	}
	return ""
}

// windowPID returns the PID stored in _NET_WM_PID, or 0 if unavailable.
func (b *X11Backend) windowPID(win xproto.Window) int32 {
	rep, err := xproto.GetProperty(b.conn, false, win, b.atomNetWMPID,
		xproto.AtomCardinal, 0, 1).Reply()
	if err != nil || len(rep.Value) < 4 {
		return 0
	}
	return int32(uint32(rep.Value[0]) | uint32(rep.Value[1])<<8 |
		uint32(rep.Value[2])<<16 | uint32(rep.Value[3])<<24)
}

// List returns all top-level windows from _NET_CLIENT_LIST.
func (b *X11Backend) List() ([]Info, error) {
	rep, err := xproto.GetProperty(b.conn, false, b.root, b.atomNetClientList,
		xproto.AtomWindow, 0, 1024).Reply()
	if err != nil {
		return nil, fmt.Errorf("window/x11: get _NET_CLIENT_LIST: %w", err)
	}
	if rep.Format != 32 {
		return nil, fmt.Errorf("window/x11: unexpected _NET_CLIENT_LIST format %d", rep.Format)
	}
	ids := make([]xproto.Window, len(rep.Value)/4)
	for i := range ids {
		ids[i] = xproto.Window(
			uint32(rep.Value[i*4]) | uint32(rep.Value[i*4+1])<<8 |
				uint32(rep.Value[i*4+2])<<16 | uint32(rep.Value[i*4+3])<<24)
	}
	infos := make([]Info, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, Info{
			ID:    uint64(id),
			Title: b.windowTitle(id),
			PID:   b.windowPID(id),
		})
	}
	return infos, nil
}

// findByTitle returns the first window whose title contains the given string (case-insensitive).
func (b *X11Backend) findByTitle(title string) (xproto.Window, error) {
	infos, err := b.List()
	if err != nil {
		return 0, err
	}
	lower := strings.ToLower(title)
	for _, info := range infos {
		if strings.Contains(strings.ToLower(info.Title), lower) {
			return xproto.Window(info.ID), nil
		}
	}
	return 0, fmt.Errorf("window/x11: window %q not found", title)
}

// Activate raises and focuses a window by title using _NET_ACTIVE_WINDOW.
func (b *X11Backend) Activate(title string) error {
	win, err := b.findByTitle(title)
	if err != nil {
		return err
	}
	data := []uint32{1, uint32(xproto.TimeCurrentTime), 0, 0, 0}
	return xproto.SendEventChecked(b.conn, false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   b.atomNetActiveWindow,
			Data:   xproto.ClientMessageDataUnionData32New(data),
		}.Bytes())).Check()
}

// Move repositions a window by title.
func (b *X11Backend) Move(title string, x, y int) error {
	win, err := b.findByTitle(title)
	if err != nil {
		return err
	}
	return xproto.ConfigureWindowChecked(b.conn, win,
		xproto.ConfigWindowX|xproto.ConfigWindowY,
		[]uint32{uint32(x), uint32(y)}).Check()
}

// Resize changes window dimensions by title.
func (b *X11Backend) Resize(title string, w, h int) error {
	win, err := b.findByTitle(title)
	if err != nil {
		return err
	}
	return xproto.ConfigureWindowChecked(b.conn, win,
		xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
		[]uint32{uint32(w), uint32(h)}).Check()
}

// ActiveTitle returns the title of the currently focused window.
func (b *X11Backend) ActiveTitle() (string, error) {
	rep, err := xproto.GetProperty(b.conn, false, b.root, b.atomNetActiveWindow,
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
func (b *X11Backend) CloseWindow(title string) error {
	win, err := b.findByTitle(title)
	if err != nil {
		return err
	}
	// Intern WM_DELETE_WINDOW and WM_PROTOCOLS atoms.
	delAtom, err := xproto.InternAtom(b.conn, false, 18, "WM_DELETE_WINDOW").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern WM_DELETE_WINDOW: %w", err)
	}
	protoAtom, err := xproto.InternAtom(b.conn, false, 12, "WM_PROTOCOLS").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern WM_PROTOCOLS: %w", err)
	}
	data := [5]uint32{uint32(delAtom.Atom), uint32(xproto.TimeCurrentTime), 0, 0, 0}
	return xproto.SendEventChecked(b.conn, false, win, 0,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   protoAtom.Atom,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check()
}

// Minimize iconifies the window by setting _NET_WM_STATE_HIDDEN via the WM.
func (b *X11Backend) Minimize(title string) error {
	win, err := b.findByTitle(title)
	if err != nil {
		return err
	}
	// Use XIconifyWindow via ChangeProperty with WM_CHANGE_STATE.
	csAtom, err := xproto.InternAtom(b.conn, false, 15, "WM_CHANGE_STATE").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern WM_CHANGE_STATE: %w", err)
	}
	data := [5]uint32{3 /* IconicState */, 0, 0, 0, 0}
	return xproto.SendEventChecked(b.conn, false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   csAtom.Atom,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check()
}

// Maximize sets _NET_WM_STATE_MAXIMIZED_VERT and _NET_WM_STATE_MAXIMIZED_HORZ.
func (b *X11Backend) Maximize(title string) error {
	win, err := b.findByTitle(title)
	if err != nil {
		return err
	}
	stateAtom, err := xproto.InternAtom(b.conn, false, 14, "_NET_WM_STATE").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE: %w", err)
	}
	maxV, err := xproto.InternAtom(b.conn, false, 28, "_NET_WM_STATE_MAXIMIZED_VERT").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE_MAXIMIZED_VERT: %w", err)
	}
	maxH, err := xproto.InternAtom(b.conn, false, 28, "_NET_WM_STATE_MAXIMIZED_HORZ").Reply()
	if err != nil {
		return fmt.Errorf("window/x11: intern _NET_WM_STATE_MAXIMIZED_HORZ: %w", err)
	}
	data := [5]uint32{1 /* _NET_WM_STATE_ADD */, uint32(maxV.Atom), uint32(maxH.Atom), 1 /* source = application */, 0}
	return xproto.SendEventChecked(b.conn, false, b.root,
		xproto.EventMaskSubstructureRedirect|xproto.EventMaskSubstructureNotify,
		string(xproto.ClientMessageEvent{
			Format: 32,
			Window: win,
			Type:   stateAtom.Atom,
			Data:   xproto.ClientMessageDataUnionData32New(data[:]),
		}.Bytes())).Check()
}
