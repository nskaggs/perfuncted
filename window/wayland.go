package window

import (
	"context"
	"fmt"
	"strings"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// WaylandWindowManager implements Manager using ext-foreign-toplevel-list-v1
// and/or zwlr-foreign-toplevel-management-v1, whichever the compositor advertises.
// Protocols are detected by probing globals at runtime; no version numbers are
// hard-coded. If neither is advertised, NewWaylandWindowManager returns an error.
//
// GNOME Wayland intentionally restricts window management access; on those
// sessions this manager will fail to initialise and Open() falls back to X11.
type WaylandWindowManager struct {
	// display is an abstraction over wl.Display so tests can inject mocks.
	display interface {
		Context() wl.Ctx
		GetRegistry() (*wl.Registry, error)
		RoundTrip() error
	}
	registry interface {
		Bind(name uint32, iface string, ver, newID uint32) error
		SetGlobalHandler(func(wl.GlobalEvent))
		ID() uint32
	}
	// underlying session (cached refcounted). If non-nil Close() should call
	// session.Close() to respect reference counting.
	session  *wl.Session
	extMgrID uint32
	wlrMgrID uint32
	// wl_seat global name (if advertised) and a bound proxy for activate requests.
	seatID    uint32
	seat      *wl.RawProxy
	toplevels map[uint32]*Info
}

func (m *WaylandWindowManager) canControlToplevels() bool {
	return m.wlrMgrID != 0
}

// NewWaylandWindowManager connects and returns a WaylandWindowManager if the
// compositor advertises at least one foreign-toplevel protocol.
func NewWaylandWindowManager() (*WaylandWindowManager, error) {
	return NewWaylandWindowManagerForSocket(wl.SocketPath())
}

// NewWaylandWindowManagerForSocket connects to sock and returns a manager if
// the compositor advertises at least one foreign-toplevel protocol.
func NewWaylandWindowManagerForSocket(sock string) (*WaylandWindowManager, error) {
	if sock == "" {
		return nil, fmt.Errorf("window/wayland: WAYLAND_DISPLAY not set")
	}

	s, err := wl.NewSession(sock)
	if err != nil {
		return nil, fmt.Errorf("window/wayland: %w", err)
	}
	m := &WaylandWindowManager{session: s, display: s.Display, registry: s.Registry, toplevels: make(map[uint32]*Info)}
	if ev, ok := s.Globals["ext_foreign_toplevel_list_v1"]; ok {
		m.extMgrID = ev.Name
	}
	if ev, ok := s.Globals["zwlr_foreign_toplevel_manager_v1"]; ok {
		m.wlrMgrID = ev.Name
	}
	if ev, ok := s.Globals["wl_seat"]; ok {
		m.seatID = ev.Name
	}
	if m.extMgrID == 0 && m.wlrMgrID == 0 {
		_ = s.Close()
		return nil, fmt.Errorf("window/wayland: neither ext_foreign_toplevel_list_v1 nor zwlr_foreign_toplevel_manager_v1 advertised (GNOME Wayland restricts this)")
	}

	// If a wl_seat global was advertised, bind a proxy so activate requests can
	// reference a valid seat object. Binding now avoids a race when Activate()
	// is called later and the caller expects the request to contain a real
	// seat object id.
	if m.seatID != 0 {
		seatProxy := &wl.RawProxy{}
		m.display.Context().Register(seatProxy)
		if err := m.registry.Bind(m.seatID, "wl_seat", 1, seatProxy.ID()); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("window/wayland: bind wl_seat: %w", err)
		}
		m.seat = seatProxy
	}

	if err := m.fetchToplevels(); err != nil {
		_ = s.Close()
		return nil, err
	}
	return m, nil
}

func (m *WaylandWindowManager) fetchToplevels() error {
	ctx := m.display.Context()
	mgrProxy := &wl.RawProxy{}
	ctx.Register(mgrProxy)

	iface, regName, ver := "ext_foreign_toplevel_list_v1", m.extMgrID, uint32(1)
	if regName == 0 {
		iface, regName, ver = "zwlr_foreign_toplevel_manager_v1", m.wlrMgrID, uint32(3)
	}
	if err := m.registry.Bind(regName, iface, ver, mgrProxy.ID()); err != nil {
		return fmt.Errorf("window/wayland: bind %s: %w", iface, err)
	}

	mgrProxy.OnEvent = func(opcode uint32, _ int, data []byte) {
		// toplevel event provides a new object id for the handle.
		if opcode != 0 || len(data) < 4 {
			return
		}
		handleID := wl.Uint32(data[0:4])
		info := &Info{ID: uint64(handleID)}
		m.toplevels[handleID] = info
		handle := &wl.RawProxy{}
		ctx.SetProxy(handleID, handle)
		// Each handle emits title/app_id/state/output_enter/leave/closed events.
		handle.OnEvent = func(op uint32, _ int, d []byte) {
			// title and app_id are strings (arg[0] = byte-length, arg[1..] = string)
			if (op == 0 || op == 1) && len(d) >= 4 {
				slen := wl.Uint32(d[0:4])
				if int(slen) <= len(d)-4 {
					info.Title = strings.TrimRight(string(d[4:4+slen]), "\x00")
				}
				return
			}
			// state is an array of uint32 and lists active states (maximized=0,
			// minimized=1, activated=2, fullscreen=3). Update the Info flags.
			if op == 4 && len(d) >= 4 {
				bytes := int(wl.Uint32(d[0:4]))
				if bytes%4 == 0 && bytes <= len(d)-4 {
					// clear previous
					info.Active = false
					info.Minimized = false
					info.Maximized = false
					info.Fullscreen = false
					n := bytes / 4
					for i := 0; i < n; i++ {
						v := wl.Uint32(d[4+i*4 : 8+i*4])
						switch v {
						case 0:
							info.Maximized = true
						case 1:
							info.Minimized = true
						case 2:
							info.Active = true
						case 3:
							info.Fullscreen = true
						}
					}
				}
				return
			}
		}
	}
	return m.display.RoundTrip()
}

// helper to send a request to a zwlr_foreign_toplevel_handle_v1 object.
func (m *WaylandWindowManager) sendHandleRequest(handleID uint32, opcode uint32, payload []byte) error {
	buf := make([]byte, 8+len(payload))
	wl.PutUint32(buf[0:], handleID)
	wl.PutUint32(buf[4:], uint32(len(buf))<<16|opcode)
	if len(payload) > 0 {
		copy(buf[8:], payload)
	}
	return m.display.Context().WriteMsg(buf, nil)
}

func (m *WaylandWindowManager) findToplevel(title string) (uint32, *Info, bool) {
	for id, info := range m.toplevels {
		if strings.Contains(strings.ToLower(info.Title), strings.ToLower(title)) {
			return id, info, true
		}
	}
	return 0, nil, false
}

// List returns all top-level windows gathered from the foreign-toplevel protocol.
// Each call performs a Wayland round-trip to process any pending events (new
// windows, title changes, closures) before returning.
func (m *WaylandWindowManager) List(ctx context.Context) ([]Info, error) {
	if err := m.display.RoundTrip(); err != nil {
		return nil, fmt.Errorf("window/wayland: round-trip: %w", err)
	}
	out := make([]Info, 0, len(m.toplevels))
	for _, v := range m.toplevels {
		out = append(out, *v)
	}
	return out, nil
}

// ActiveTitle returns the title of the currently focused window, if available.
func (m *WaylandWindowManager) ActiveTitle(ctx context.Context) (string, error) {
	if err := m.display.RoundTrip(); err != nil {
		return "", fmt.Errorf("window/wayland: round-trip: %w", err)
	}
	for _, v := range m.toplevels {
		if v.Active {
			return v.Title, nil
		}
	}
	return "", ErrNotSupported
}

// Activate raises a window by title substring. Activation requires a Wayland
// seat and the zwlr foreign-toplevel control protocol; ext_foreign_toplevel_list_v1
// is enumeration-only and cannot activate windows.
func (m *WaylandWindowManager) Activate(ctx context.Context, title string) error {
	if err := m.display.RoundTrip(); err != nil {
		return fmt.Errorf("window/wayland: round-trip: %w", err)
	}
	id, _, ok := m.findToplevel(title)
	if !ok {
		return fmt.Errorf("window/wayland: window matching %q not found", title)
	}
	if !m.canControlToplevels() {
		return ErrNotSupported
	}
	if m.seat == nil {
		// Try binding a seat now if we know the global name.
		if m.seatID == 0 {
			return ErrNotSupported
		}
		seatProxy := &wl.RawProxy{}
		m.display.Context().Register(seatProxy)
		if err := m.registry.Bind(m.seatID, "wl_seat", 1, seatProxy.ID()); err != nil {
			return fmt.Errorf("window/wayland: bind wl_seat: %w", err)
		}
		m.seat = seatProxy
		if err := m.display.RoundTrip(); err != nil {
			return fmt.Errorf("window/wayland: round-trip: %w", err)
		}
	}
	payload := make([]byte, 4)
	wl.PutUint32(payload, m.seat.ID())
	if err := m.sendHandleRequest(id, 4, payload); err != nil {
		return fmt.Errorf("window/wayland: activate: %w", err)
	}
	return nil
}

// Restore unsets maximized and minimized state on the matching toplevel.
func (m *WaylandWindowManager) Restore(ctx context.Context, title string) error {
	if err := m.display.RoundTrip(); err != nil {
		return fmt.Errorf("window/wayland: round-trip: %w", err)
	}
	info, err := FindByTitle(ctx, m, title)
	if err != nil {
		return fmt.Errorf("window/wayland: %w", err)
	}
	id := uint32(info.ID)
	if !m.canControlToplevels() {
		return ErrNotSupported
	}
	// unset_maximized (1) + unset_minimized (3)
	if err := m.sendHandleRequest(id, 1, nil); err != nil {
		return fmt.Errorf("window/wayland: unset_maximized: %w", err)
	}
	if err := m.sendHandleRequest(id, 3, nil); err != nil {
		return fmt.Errorf("window/wayland: unset_minimized: %w", err)
	}
	return nil
}

// Move returns ErrNotSupported; Wayland does not allow clients to reposition native windows.
func (m *WaylandWindowManager) Move(ctx context.Context, _ string, _, _ int) error {
	return ErrNotSupported
}

// Resize returns ErrNotSupported; Wayland does not allow clients to resize native windows.
func (m *WaylandWindowManager) Resize(ctx context.Context, _ string, _, _ int) error {
	return ErrNotSupported
}

// CloseWindow requests that the toplevel close itself via the foreign-toplevel protocol.
func (m *WaylandWindowManager) CloseWindow(ctx context.Context, title string) error {
	if err := m.display.RoundTrip(); err != nil {
		return fmt.Errorf("window/wayland: round-trip: %w", err)
	}
	info, err := FindByTitle(ctx, m, title)
	if err != nil {
		return fmt.Errorf("window/wayland: %w", err)
	}
	id := uint32(info.ID)
	if !m.canControlToplevels() {
		return ErrNotSupported
	}
	if err := m.sendHandleRequest(id, 5, nil); err != nil {
		return fmt.Errorf("window/wayland: close: %w", err)
	}
	return nil
}

// Minimize requests the compositor to minimize the matching toplevel. This is
// only available on zwlr_foreign_toplevel_manager_v1.
func (m *WaylandWindowManager) Minimize(ctx context.Context, title string) error {
	if err := m.display.RoundTrip(); err != nil {
		return fmt.Errorf("window/wayland: round-trip: %w", err)
	}
	info, err := FindByTitle(ctx, m, title)
	if err != nil {
		return fmt.Errorf("window/wayland: %w", err)
	}
	id := uint32(info.ID)
	if !m.canControlToplevels() {
		return ErrNotSupported
	}
	if err := m.sendHandleRequest(id, 2, nil); err != nil {
		return fmt.Errorf("window/wayland: minimize: %w", err)
	}
	return nil
}

// Maximize requests the compositor to maximize the matching toplevel. This is
// only available on zwlr_foreign_toplevel_manager_v1.
func (m *WaylandWindowManager) Maximize(ctx context.Context, title string) error {
	if err := m.display.RoundTrip(); err != nil {
		return fmt.Errorf("window/wayland: round-trip: %w", err)
	}
	info, err := FindByTitle(ctx, m, title)
	if err != nil {
		return fmt.Errorf("window/wayland: %w", err)
	}
	id := uint32(info.ID)
	if !m.canControlToplevels() {
		return ErrNotSupported
	}
	if err := m.sendHandleRequest(id, 0, nil); err != nil {
		return fmt.Errorf("window/wayland: maximize: %w", err)
	}
	return nil
}

func (m *WaylandWindowManager) Close() error {
	if m.session != nil {
		return m.session.Close()
	}
	if m.display != nil {
		return m.display.Context().Close()
	}
	return nil
}
