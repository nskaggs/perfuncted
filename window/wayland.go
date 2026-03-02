package window

import (
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
	display   *wl.Display
	registry  *wl.Registry
	extMgrID  uint32
	wlrMgrID  uint32
	toplevels map[uint32]*Info
}

// NewWaylandWindowManager connects and returns a WaylandWindowManager if the
// compositor advertises at least one foreign-toplevel protocol.
func NewWaylandWindowManager() (*WaylandWindowManager, error) {
	sock := wl.SocketPath()
	if sock == "" {
		return nil, fmt.Errorf("window/wayland: WAYLAND_DISPLAY not set")
	}
	ctx, err := wl.Connect(sock)
	if err != nil {
		return nil, fmt.Errorf("window/wayland: connect: %w", err)
	}
	display := wl.NewDisplay(ctx)
	registry, err := display.GetRegistry()
	if err != nil {
		ctx.Close()
		return nil, fmt.Errorf("window/wayland: get registry: %w", err)
	}
	m := &WaylandWindowManager{display: display, registry: registry, toplevels: make(map[uint32]*Info)}
	registry.SetGlobalHandler(func(ev wl.GlobalEvent) {
		switch ev.Interface {
		case "ext_foreign_toplevel_list_v1":
			m.extMgrID = ev.Name
		case "zwlr_foreign_toplevel_manager_v1":
			m.wlrMgrID = ev.Name
		}
	})
	if err := display.RoundTrip(); err != nil {
		ctx.Close()
		return nil, fmt.Errorf("window/wayland: registry round-trip: %w", err)
	}
	if m.extMgrID == 0 && m.wlrMgrID == 0 {
		ctx.Close()
		return nil, fmt.Errorf("window/wayland: neither ext_foreign_toplevel_list_v1 nor zwlr_foreign_toplevel_manager_v1 advertised (GNOME Wayland restricts this)")
	}
	if err := m.fetchToplevels(); err != nil {
		ctx.Close()
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
		if opcode != 0 || len(data) < 4 {
			return
		}
		handleID := wl.Uint32(data[0:4])
		info := &Info{ID: uint64(handleID)}
		m.toplevels[handleID] = info
		handle := &wl.RawProxy{}
		ctx.SetProxy(handleID, handle)
		handle.OnEvent = func(op uint32, _ int, d []byte) {
			if (op == 0 || op == 1) && len(d) >= 4 {
				slen := wl.Uint32(d[0:4])
				if int(slen) <= len(d)-4 {
					info.Title = strings.TrimRight(string(d[4:4+slen]), "\x00")
				}
			}
		}
	}
	return m.display.RoundTrip()
}

// List returns all top-level windows gathered from the foreign-toplevel protocol.
func (m *WaylandWindowManager) List() ([]Info, error) {
	out := make([]Info, 0, len(m.toplevels))
	for _, v := range m.toplevels {
		out = append(out, *v)
	}
	return out, nil
}

// ActiveTitle returns ErrNotSupported; compositors do not expose active-window
// focus over the foreign-toplevel management protocol without a seat.
func (m *WaylandWindowManager) ActiveTitle() (string, error) {
	return "", ErrNotSupported
}

// Activate raises a window by title substring. Activation requires a Wayland
// seat/serial not available without a compositor-owned surface, so this returns
// ErrNotSupported. Use KWinWindowManager.Activate() on KDE sessions instead.
func (m *WaylandWindowManager) Activate(_ string) error {
	return ErrNotSupported
}

// Move returns ErrNotSupported; Wayland does not allow clients to reposition windows.
func (m *WaylandWindowManager) Move(_ string, _, _ int) error { return ErrNotSupported }

// Resize returns ErrNotSupported; Wayland does not allow clients to resize windows.
func (m *WaylandWindowManager) Resize(_ string, _, _ int) error { return ErrNotSupported }

func (m *WaylandWindowManager) Close() error { return m.display.Context().Close() }
