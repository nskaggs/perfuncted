// Package window provides window management backends for X11 and Wayland.
package window

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// ErrNotSupported is returned when the backend cannot perform an operation.
var ErrNotSupported = errors.New("window: operation not supported on this compositor")

// Info describes a managed window.
// Note: Geometry fields (X,Y,W,H) are best-effort. Wayland's foreign-toplevel
// protocols do not always provide bounds; backends may leave them zero. Do not
// rely on these fields being present for all compositors — treat them as
// advisory. For Wayland, clients requiring accurate geometry should use a
// compositor-specific protocol (xdg-output) when available.
type Info struct {
	ID    uint64
	Title string
	PID   int32
	X, Y  int
	W, H  int
	// Runtime state gathered from the foreign-toplevel protocol.
	Active     bool
	Minimized  bool
	Maximized  bool
	Fullscreen bool
}

// Manager lists and controls desktop windows.
//
// All title-based methods use case-insensitive substring matching; the
// first window whose title contains the given string is acted upon.
type Manager interface {
	// List returns all visible top-level windows.
	List(ctx context.Context) ([]Info, error)
	// Activate brings the window matching title to the foreground.
	Activate(ctx context.Context, title string) error
	// Move repositions the window matching title to (x, y).
	Move(ctx context.Context, title string, x, y int) error
	// Resize changes the dimensions of the window matching title.
	Resize(ctx context.Context, title string, w, h int) error
	// ActiveTitle returns the title of the currently focused window.
	ActiveTitle(ctx context.Context) (string, error)
	// CloseWindow closes the window matching title.
	CloseWindow(ctx context.Context, title string) error
	// Minimize minimizes the window matching title.
	Minimize(ctx context.Context, title string) error
	// Maximize maximizes the window matching title.
	Maximize(ctx context.Context, title string) error
	// Restore restores the window matching title.
	//
	// The semantics are backend-defined but should aim to return the window to
	// a normal (unminimized, unmaximized) visible state. If a backend cannot
	// perform a restore, it should return ErrNotSupported.
	Restore(ctx context.Context, title string) error
	// Close releases backend resources.
	Close() error
}

// Open returns the best available Manager for the current environment.
func Open() (Manager, error) {
	return OpenRuntime(env.Current())
}

// OpenRuntime returns the best available Manager for rt.
func OpenRuntime(rt env.Runtime) (Manager, error) {
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
			return nil, fmt.Errorf("window: GNOME Shell Eval not available: %w", err)
		}
		return m, nil

	case compositor.Unknown:
		if m, err := NewWaylandWindowManagerForSocket(rt.SocketPath()); err == nil {
			return m, nil
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
		r.Reason = "unsafe mode disabled or access denied"
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
