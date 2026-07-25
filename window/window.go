// Package window provides window management backends for X11 and Wayland.
package window

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strconv"
	"strings"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// ErrNotSupported is returned when the backend cannot perform an operation.
var ErrNotSupported = errors.New("window: operation not supported on this compositor")

// ErrWindowNotFound is returned when a window matching the requested title or
// criteria could not be located.
var ErrWindowNotFound = errors.New("window: not found")

// ErrWindowAmbiguous is returned when a match identifies multiple windows.
var ErrWindowAmbiguous = errors.New("window: ambiguous match")

// Info describes a managed window.
// Note: Geometry fields (X,Y,W,H) are best-effort. Wayland's foreign-toplevel
// protocols do not always provide bounds; backends may leave them zero. Do not
// rely on these fields being present for all compositors — treat them as
// advisory. For Wayland, clients requiring accurate geometry should use a
// compositor-specific protocol (xdg-output) when available.
type Info struct {
	// ID preserves the backend's numeric identifier when one exists.
	ID uint64
	// NativeID is the authoritative backend identifier. It is numeric text for
	// X11, Sway, GNOME, and foreign-toplevel backends, and may be an opaque
	// string such as a KWin UUID.
	NativeID string
	Title    string
	AppID    string
	Class    string
	PID      int32
	X, Y     int
	W, H     int
	// Runtime state gathered from the foreign-toplevel protocol.
	Active     bool
	Minimized  bool
	Maximized  bool
	Fullscreen bool
}

// Manager discovers desktop windows.
type Manager interface {
	// List returns all visible top-level windows.
	List(ctx context.Context) ([]Info, error)
	// IterateWindows returns an iterator over all visible top-level windows.
	IterateWindows(ctx context.Context) iter.Seq2[Info, error]
	// ActiveTitle returns the title of the currently focused window.
	ActiveTitle(ctx context.Context) (string, error)
	// Close releases backend resources.
	Close() error
}

// IDManager controls windows by stable native ID. Runtime backends returned by
// OpenRuntime implement this interface; it is separate from Manager so
// discovery-only fakes and integrations do not need meaningless control
// methods.
type IDManager interface {
	Manager
	// Handle-based operations target windows by their stable ID rather than
	// title substring. These avoid ambiguity when multiple windows share a
	// title prefix. Backends that cannot resolve a window by ID should return
	// ErrWindowNotFound.
	ActivateByID(ctx context.Context, id string) error
	MoveByID(ctx context.Context, id string, x, y int) error
	ResizeByID(ctx context.Context, id string, w, h int) error
	CloseWindowByID(ctx context.Context, id string) error
	MinimizeByID(ctx context.Context, id string) error
	MaximizeByID(ctx context.Context, id string) error
	FullscreenByID(ctx context.Context, id string) error
	UnfullscreenByID(ctx context.Context, id string) error
	RestoreByID(ctx context.Context, id string) error
	// InfoByID returns fresh window info for the given ID.
	InfoByID(ctx context.Context, id string) (Info, error)
	// WaitClosedByID blocks until the window with the given ID disappears.
	WaitClosedByID(ctx context.Context, id string) error
}

// OperationReporter advertises the operations implemented by a window
// backend. Discovery is always present for a usable Manager.
type OperationReporter interface {
	SupportedOperations() []string
}

// NativeID returns the stable backend identifier for info.
func (info Info) StableID() string {
	if info.NativeID != "" {
		return info.NativeID
	}
	return fmt.Sprintf("%d", info.ID)
}

func numericID(id string) (uint64, error) {
	value, err := strconv.ParseUint(id, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("window: invalid numeric id %q: %w", id, err)
	}
	return value, nil
}

// OpenRuntime returns the best available Manager for rt.
func OpenRuntime(rt env.Runtime) (Manager, error) {
	if display := rt.Display(); display != "" && rt.SocketPath() == "" {
		return NewX11Backend(display)
	}
	switch compositor.DetectRuntime(rt) {
	case compositor.KDE:
		m, err := NewKWinScriptManagerForBus(rt.Get("DBUS_SESSION_BUS_ADDRESS"))
		if err != nil {
			return nil, fmt.Errorf("window: KDE detected but KWin scripting unavailable: %w", err)
		}
		return m, nil

	case compositor.Wlroots:
		if m, err := NewSwayManagerRuntime(rt); err == nil {
			return m, nil
		}
		m, err := NewWaylandWindowManagerForSocket(rt.SocketPath())
		if err != nil {
			return nil, fmt.Errorf("window: no window manager available on this wlroots compositor: %w", err)
		}
		return m, nil

	case compositor.GNOME:
		m, err := NewGnomeManagerForBus(rt.Get("DBUS_SESSION_BUS_ADDRESS"))
		if err != nil {
			return nil, fmt.Errorf("window: GNOME Shell Eval unavailable (unsafe mode required): %w", err)
		}
		return m, nil

	case compositor.Unknown:
		if m, err := NewWaylandWindowManagerForSocket(rt.SocketPath()); err == nil {
			return m, nil
		}
		if display := rt.Display(); display != "" {
			return NewX11Backend(display)
		}
		return nil, fmt.Errorf("window: unsupported Wayland compositor")

	default: // X11 / XWayland
		d := rt.Display()
		if d == "" {
			return nil, fmt.Errorf("window: no display (set WAYLAND_DISPLAY or DISPLAY)")
		}
		return NewX11Backend(d)
	}
}

// Probe returns availability details for each window backend in priority order.
func Probe() []probe.Result {
	return ProbeRuntime(env.Current())
}

// ProbeRuntime returns availability details for rt in backend priority order.
func ProbeRuntime(rt env.Runtime) []probe.Result {
	kind := compositor.DetectRuntime(rt)
	globals := wl.ListGlobals(rt.SocketPath())

	return probe.SelectBest([]probe.Result{
		checkKWinScript(rt, kind),
		checkGnomeShellEval(rt, kind),
		checkForeignToplevel(globals),
	})
}

func checkKWinScript(rt env.Runtime, kind compositor.Session) probe.Result {
	r := probe.Result{Name: "kwin-scripting"}
	if kind != compositor.KDE {
		r.Reason = "not a KDE Plasma session"
		return r
	}
	if rt.Get("DBUS_SESSION_BUS_ADDRESS") == "" {
		r.Reason = "D-Bus unavailable"
		return r
	}
	conn, err := dbusutil.SessionBusAddress(rt.Get("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		r.Reason = fmt.Sprintf("D-Bus unavailable: %v", err)
		return r
	}
	defer conn.Close()
	var intro string
	obj := conn.Object("org.kde.KWin", "/Scripting")
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&intro); err != nil {
		r.Reason = fmt.Sprintf("org.kde.kwin.Scripting not accessible: %v", err)
		return r
	}
	if strings.Contains(intro, "org.kde.kwin.Scripting") {
		r.Available = true
		r.Reason = "org.kde.kwin.Scripting accessible"
	} else {
		r.Reason = "org.kde.kwin.Scripting interface absent"
	}
	return r
}

func checkGnomeShellEval(rt env.Runtime, kind compositor.Session) probe.Result {
	r := probe.Result{Name: "gnome-shell-eval"}
	if kind != compositor.GNOME {
		r.Reason = "not a GNOME session"
		return r
	}
	g, err := NewGnomeManagerForBus(rt.Get("DBUS_SESSION_BUS_ADDRESS"))
	if err != nil {
		r.Reason = err.Error()
		return r
	}
	g.Close()
	r.Available = true
	r.Reason = "org.gnome.Shell.Eval on session bus (unsafe mode)"
	return r
}

func checkForeignToplevel(globals map[string]bool) probe.Result {
	r := probe.Result{Name: "foreign-toplevel"}
	if globals == nil {
		r.Reason = "no Wayland session"
		return r
	}
	if globals["zwlr_foreign_toplevel_manager_v1"] {
		r.Available = true
		r.Reason = "zwlr_foreign_toplevel_manager_v1 advertised"
		return r
	}
	if globals["ext_foreign_toplevel_list_v1"] {
		r.Available = true
		r.Reason = "ext_foreign_toplevel_list_v1 advertised"
		return r
	}
	r.Reason = "no foreign-toplevel protocol advertised"
	return r
}
