package window

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"strings"
	"sync"

	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/wl"
)

var _ Manager = (*WaylandWindowManager)(nil)

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
	// toplevelsMu protects toplevels map.
	toplevelsMu sync.Mutex
	changesOnce sync.Once
	changes     chan struct{}
}

func (m *WaylandWindowManager) canControlToplevels() bool {
	return m.wlrMgrID != 0
}

func applyToplevelString(info *Info, opcode uint32, data []byte) bool {
	if len(data) < 4 {
		return false
	}
	slen := wl.Uint32(data[0:4])
	if int(slen) > len(data)-4 {
		return false
	}
	value := strings.TrimRight(string(data[4:4+slen]), "\x00")
	switch opcode {
	case 0:
		info.Title = value
	case 1:
		info.AppID = value
	default:
		return false
	}
	return true
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
	initErr := wl.WithOperation(m.display.Context(), func() error {
		if m.seatID != 0 {
			seatProxy := &wl.RawProxy{}
			m.display.Context().Register(seatProxy)
			if err := m.registry.Bind(m.seatID, "wl_seat", 1, seatProxy.ID()); err != nil {
				return fmt.Errorf("window/wayland: bind wl_seat: %w", err)
			}
			m.seat = seatProxy
		}
		return m.fetchToplevels()
	})
	if initErr != nil {
		_ = s.Close()
		return nil, initErr
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
	isWLR := iface == "zwlr_foreign_toplevel_manager_v1"
	if err := m.registry.Bind(regName, iface, ver, mgrProxy.ID()); err != nil {
		return fmt.Errorf("window/wayland: bind %s: %w", iface, err)
	}

	mgrProxy.OnEvent = func(opcode uint32, _ int, data []byte) {
		// toplevel event provides a new object id for the handle.
		if opcode != 0 || len(data) < 4 {
			return
		}
		handleID := wl.Uint32(data[0:4])
		info := &Info{
			ID:       uint64(handleID),
			NativeID: strconv.FormatUint(uint64(handleID), 10),
		}
		m.toplevelsMu.Lock()
		m.toplevels[handleID] = info
		m.toplevelsMu.Unlock()
		m.notifyWindowChange()
		handle := &wl.RawProxy{}
		ctx.SetProxy(handleID, handle)
		// Each handle emits title/app_id/state/output_enter/leave/closed events.
		handle.OnEvent = func(op uint32, _ int, d []byte) {
			if !isWLR {
				m.handleExtToplevelEvent(handleID, info, op, d)
				return
			}
			m.handleWLRToplevelEvent(handleID, info, op, d)
		}
	}
	return m.display.RoundTrip()
}

func (m *WaylandWindowManager) handleWLRToplevelEvent(
	handleID uint32,
	info *Info,
	op uint32,
	data []byte,
) {
	m.toplevelsMu.Lock()
	defer m.toplevelsMu.Unlock()
	defer m.notifyWindowChange()

	// title and app_id are strings (arg[0] = byte-length, arg[1..] = string)
	if applyToplevelString(info, op, data) {
		return
	}
	// state is an array of uint32 and lists active states (maximized=0,
	// minimized=1, activated=2, fullscreen=3). Update the Info flags.
	if op == 4 && len(data) >= 4 {
		bytes := int(wl.Uint32(data[0:4]))
		if bytes%4 == 0 && bytes <= len(data)-4 {
			// state is an array of uint32 and lists active states (maximized=0,
			// minimized=1, activated=2, fullscreen=3).
			info.Active = false
			info.Minimized = false
			info.Maximized = false
			info.Fullscreen = false
			n := bytes / 4
			for i := 0; i < n; i++ {
				value := wl.Uint32(data[4+i*4 : 8+i*4])
				switch value {
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
	}
	if op == 6 {
		delete(m.toplevels, handleID)
	}
}

func (m *WaylandWindowManager) handleExtToplevelEvent(
	handleID uint32,
	info *Info,
	op uint32,
	data []byte,
) {
	m.toplevelsMu.Lock()
	defer m.toplevelsMu.Unlock()
	defer m.notifyWindowChange()

	switch op {
	case 0: // closed
		delete(m.toplevels, handleID)
	case 2: // title
		info.Title = decodeWaylandString(data)
	case 3: // app_id
		info.AppID = decodeWaylandString(data)
	case 4: // stable protocol identifier
		if identifier := decodeWaylandString(data); identifier != "" {
			info.NativeID = identifier
		}
	}
}

func decodeWaylandString(data []byte) string {
	if len(data) < 4 {
		return ""
	}
	length := int(wl.Uint32(data[:4]))
	if length > len(data)-4 {
		return ""
	}
	return strings.TrimRight(string(data[4:4+length]), "\x00")
}

func (m *WaylandWindowManager) notifyWindowChange() {
	m.changesOnce.Do(func() {
		m.changes = make(chan struct{}, 1)
	})
	select {
	case m.changes <- struct{}{}:
	default:
	}
}

// WindowChanges exposes coalesced foreign-toplevel protocol hints.
func (m *WaylandWindowManager) WindowChanges() <-chan struct{} {
	m.changesOnce.Do(func() {
		m.changes = make(chan struct{}, 1)
	})
	return m.changes
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

func (m *WaylandWindowManager) withOperation(fn func() error) error {
	if m == nil || m.display == nil {
		return fmt.Errorf("window/wayland: manager not initialised")
	}
	return wl.WithOperation(m.display.Context(), fn)
}

func (m *WaylandWindowManager) List(ctx context.Context) ([]Info, error) {
	var out []Info
	for win, err := range m.IterateWindows(ctx) {
		if err != nil {
			return nil, err
		}
		out = append(out, win)
	}
	return out, nil
}

// IterateWindows returns an iterator over all top-level windows.
func (m *WaylandWindowManager) IterateWindows(ctx context.Context) iter.Seq2[Info, error] {
	return func(yield func(Info, error) bool) {
		ctx = contextutil.Default(ctx)
		if err := ctx.Err(); err != nil {
			yield(Info{}, fmt.Errorf("window/wayland: iterate canceled: %w", err))
			return
		}
		var windows []Info
		if err := m.withOperation(func() error {
			if err := m.display.RoundTrip(); err != nil {
				return fmt.Errorf("window/wayland: round-trip: %w", err)
			}
			m.toplevelsMu.Lock()
			defer m.toplevelsMu.Unlock()
			windows = make([]Info, 0, len(m.toplevels))
			for _, v := range m.toplevels {
				windows = append(windows, *v)
			}
			return nil
		}); err != nil {
			yield(Info{}, err)
			return
		}
		for _, v := range windows {
			if !yield(v, nil) {
				return
			}
		}
	}
}

// ActiveTitle returns the title of the currently focused window, if available.
func (m *WaylandWindowManager) ActiveTitle(ctx context.Context) (string, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("window/wayland: active title canceled: %w", err)
	}
	var title string
	err := m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return fmt.Errorf("window/wayland: round-trip: %w", err)
		}
		m.toplevelsMu.Lock()
		defer m.toplevelsMu.Unlock()
		for _, v := range m.toplevels {
			if v.Active {
				title = v.Title
				return nil
			}
		}
		return ErrNotSupported
	})
	return title, err
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

func (m *WaylandWindowManager) Sync(ctx context.Context) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/wayland: sync canceled: %w", err)
	}
	if m.display == nil {
		return nil
	}
	return m.withOperation(m.display.RoundTrip)
}

func (m *WaylandWindowManager) SupportedOperations() []string {
	if !m.canControlToplevels() {
		return []string{"discover"}
	}
	return []string{
		"discover",
		"activate",
		"close",
		"minimize",
		"maximize",
		"fullscreen",
		"restore",
	}
}

// --- Handle-based operations ---

func (m *WaylandWindowManager) lookupByID(id string) (uint32, Info, error) {
	m.toplevelsMu.Lock()
	defer m.toplevelsMu.Unlock()
	for handleID, info := range m.toplevels {
		if info.StableID() == id {
			return handleID, *info, nil
		}
	}
	return 0, Info{}, ErrWindowNotFound
}

func (m *WaylandWindowManager) ActivateByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/wayland: activate canceled: %w", err)
	}
	return m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return err
		}
		hid, _, err := m.lookupByID(id)
		if err != nil {
			return err
		}
		if !m.canControlToplevels() {
			return ErrNotSupported
		}
		if m.seat == nil {
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
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("window/wayland: activate canceled: %w", err)
		}
		payload := make([]byte, 4)
		wl.PutUint32(payload, m.seat.ID())
		return m.sendHandleRequest(hid, 4, payload)
	})
}

func (m *WaylandWindowManager) MoveByID(_ context.Context, _ string, _, _ int) error {
	return ErrNotSupported
}

func (m *WaylandWindowManager) ResizeByID(_ context.Context, _ string, _, _ int) error {
	return ErrNotSupported
}

func (m *WaylandWindowManager) CloseWindowByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/wayland: close canceled: %w", err)
	}
	return m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return err
		}
		hid, _, err := m.lookupByID(id)
		if err != nil {
			return err
		}
		if !m.canControlToplevels() {
			return ErrNotSupported
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("window/wayland: close canceled: %w", err)
		}
		return m.sendHandleRequest(hid, 5, nil)
	})
}

func (m *WaylandWindowManager) MinimizeByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/wayland: minimize canceled: %w", err)
	}
	return m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return err
		}
		hid, _, err := m.lookupByID(id)
		if err != nil {
			return err
		}
		if !m.canControlToplevels() {
			return ErrNotSupported
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("window/wayland: minimize canceled: %w", err)
		}
		return m.sendHandleRequest(hid, 2, nil)
	})
}

func (m *WaylandWindowManager) MaximizeByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/wayland: maximize canceled: %w", err)
	}
	return m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return err
		}
		hid, _, err := m.lookupByID(id)
		if err != nil {
			return err
		}
		if !m.canControlToplevels() {
			return ErrNotSupported
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("window/wayland: maximize canceled: %w", err)
		}
		return m.sendHandleRequest(hid, 0, nil)
	})
}

func (m *WaylandWindowManager) FullscreenByID(
	ctx context.Context,
	id string,
) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/wayland: fullscreen canceled: %w", err)
	}
	return m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return err
		}
		handleID, _, err := m.lookupByID(id)
		if err != nil {
			return err
		}
		if !m.canControlToplevels() {
			return ErrNotSupported
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("window/wayland: fullscreen canceled: %w", err)
		}
		// A null wl_output lets the compositor choose the target output.
		return m.sendHandleRequest(handleID, 8, make([]byte, 4))
	})
}

func (m *WaylandWindowManager) UnfullscreenByID(
	ctx context.Context,
	id string,
) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/wayland: unfullscreen canceled: %w", err)
	}
	return m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return err
		}
		handleID, _, err := m.lookupByID(id)
		if err != nil {
			return err
		}
		if !m.canControlToplevels() {
			return ErrNotSupported
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("window/wayland: unfullscreen canceled: %w", err)
		}
		return m.sendHandleRequest(handleID, 9, nil)
	})
}

func (m *WaylandWindowManager) RestoreByID(ctx context.Context, id string) error {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("window/wayland: restore canceled: %w", err)
	}
	return m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return err
		}
		hid, _, err := m.lookupByID(id)
		if err != nil {
			return err
		}
		if !m.canControlToplevels() {
			return ErrNotSupported
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("window/wayland: restore canceled: %w", err)
		}
		if err := m.sendHandleRequest(hid, 1, nil); err != nil {
			return err
		}
		return m.sendHandleRequest(hid, 3, nil)
	})
}

func (m *WaylandWindowManager) InfoByID(ctx context.Context, id string) (Info, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return Info{}, fmt.Errorf("window/wayland: info canceled: %w", err)
	}
	var info Info
	err := m.withOperation(func() error {
		if err := m.display.RoundTrip(); err != nil {
			return err
		}
		_, found, err := m.lookupByID(id)
		if err != nil {
			return err
		}
		info = found
		return nil
	})
	return info, err
}
