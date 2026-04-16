// Package window provides window management backends for X11 and Wayland.
package window

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// ErrNotSupported is returned when the backend cannot perform an operation.
var ErrNotSupported = errors.New("window: operation not supported on this compositor")

// Info describes a managed window.
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
	List() ([]Info, error)
	// Activate brings the window matching title to the foreground.
	Activate(title string) error
	// Move repositions the window matching title to (x, y).
	Move(title string, x, y int) error
	// Resize changes the dimensions of the window matching title.
	Resize(title string, w, h int) error
	// ActiveTitle returns the title of the currently focused window.
	ActiveTitle() (string, error)
	// CloseWindow closes the window matching title.
	CloseWindow(title string) error
	// Minimize minimizes the window matching title.
	Minimize(title string) error
	// Maximize maximizes the window matching title.
	Maximize(title string) error
	// Close releases backend resources.
	Close() error
}

// Open returns the best available Manager for the current environment.
func Open() (Manager, error) {
	switch compositor.Detect() {
	case compositor.KDE:
		m, err := NewKWinScriptManager()
		if err != nil {
			return nil, fmt.Errorf("window: KDE detected but KWin scripting unavailable: %w", err)
		}
		return m, nil

	case compositor.Wlroots:
		if m, err := NewSwayManager(); err == nil {
			return m, nil
		}
		m, err := NewWaylandWindowManager()
		if err != nil {
			return nil, fmt.Errorf("window: no window manager available on this wlroots compositor: %w", err)
		}
		return m, nil

	case compositor.GNOME:
		m, err := NewGnomeManager()
		if err != nil {
			return nil, fmt.Errorf("window: GNOME Shell Eval not available: %w", err)
		}
		return m, nil

	case compositor.Unknown:
		if m, err := NewWaylandWindowManager(); err == nil {
			return m, nil
		}
		return nil, fmt.Errorf("window: unsupported Wayland compositor")

	default: // X11 / XWayland
		d := os.Getenv("DISPLAY")
		if d == "" {
			return nil, fmt.Errorf("window: no display (set WAYLAND_DISPLAY or DISPLAY)")
		}
		return NewX11Backend(d)
	}
}

// Probe returns availability details for each window backend in priority order.
func Probe() []probe.Result {
	kind := compositor.Detect()
	globals := wl.ListGlobals(wl.SocketPath())

	return probe.SelectBest([]probe.Result{
		checkKWinScript(kind),
		checkForeignToplevel(globals),
	})
}

func checkKWinScript(kind compositor.Session) probe.Result {
	r := probe.Result{Name: "kwin-scripting"}
	if kind != compositor.KDE {
		r.Reason = "not a KDE Plasma session"
		return r
	}
	conn, err := dbus.SessionBus()
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
