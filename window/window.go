// Package window provides window management backends for X11 and Wayland.
package window

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
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
	// Close releases backend resources.
	Close() error
}

// Cached backend detection results
var (
	cachedWindow   Manager
	cachedWindowMu sync.Mutex
	prevWindowEnv  string
)

// Open returns the best available Manager for the current environment.
// Uses caching to avoid repeated backend detection and prioritizes
// backends based on detected session type.
func Open() (Manager, error) {
	// Check cache first and invalidate on environment change.
	currentFP := windowEnvFingerprint()
	cachedWindowMu.Lock()
	if cachedWindow != nil {
		if prevWindowEnv == currentFP {
			defer cachedWindowMu.Unlock()
			return cachedWindow, nil
		}
		cachedWindow.Close()
		cachedWindow = nil
	}
	cachedWindowMu.Unlock()

	// Determine session type for prioritized backend selection
	session := compositor.Detect()
	var m Manager
	var err error

	switch session {
	case compositor.KDE:
		m, err = NewKWinScriptManager()
		if err != nil {
			return nil, fmt.Errorf("window: KDE detected but KWin scripting unavailable: %w", err)
		}

	case compositor.Wlroots:
		if m, err = NewSwayManager(); err == nil {
			// Successfully got SwayManager
		} else {
			m, err = NewWaylandWindowManager()
			if err != nil {
				return nil, fmt.Errorf("window: no window manager available on this wlroots compositor: %w", err)
			}
		}

	case compositor.GNOME:
		m, err = NewGnomeManager()
		if err != nil {
			return nil, fmt.Errorf("window: GNOME Shell Eval not available: %w", err)
		}

	case compositor.Unknown:
		if m, err = NewWaylandWindowManager(); err == nil {
			// Successfully got WaylandWindowManager
		} else {
			return nil, fmt.Errorf("window: unsupported Wayland compositor")
		}

	default: // X11 / XWayland
		d := displayEnv()
		if d == "" {
			return nil, fmt.Errorf("window: no display (set WAYLAND_DISPLAY or DISPLAY)")
		}
		m, err = NewX11Backend(d)
	}

	if m != nil {
		cachedWindowMu.Lock()
		cachedWindow = m
		prevWindowEnv = currentFP
		cachedWindowMu.Unlock()
	}

	return m, err
}

var displayOverride string

// SetDisplayOverride sets an explicit DISPLAY value for package-level lookups.
func SetDisplayOverride(d string) { displayOverride = d }

func displayEnv() string {
	if displayOverride != "" {
		return displayOverride
	}
	return os.Getenv("DISPLAY")
}

func windowEnvFingerprint() string {
	return os.Getenv("XDG_RUNTIME_DIR") + "|" + os.Getenv("WAYLAND_DISPLAY") + "|" + os.Getenv("DBUS_SESSION_BUS_ADDRESS") + "|" + displayEnv()
}

// Probe returns availability details for each window backend in priority order.
func Probe() []probe.Result {
	kind := compositor.Detect()
	globals := wl.ListGlobals(wl.SocketPath())

	return probe.SelectBest([]probe.Result{
		checkKWinScript(kind),
		checkGnomeShellEval(kind),
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

func checkGnomeShellEval(kind compositor.Session) probe.Result {
	r := probe.Result{Name: "gnome-shell-eval"}
	if kind != compositor.GNOME {
		r.Reason = "not a GNOME session"
		return r
	}
	g, err := NewGnomeManager()
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
