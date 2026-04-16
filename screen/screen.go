// Package screen provides screen capture backends for X11 and Wayland.
package screen

import (
	"fmt"
	"image"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/probe"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// Screenshotter captures a rectangular region of the screen.
type Screenshotter interface {
	Grab(rect image.Rectangle) (image.Image, error)
	Close() error
}

// Resolver reports the output resolution. Backends that track output geometry
// (e.g. via wl_output mode events) implement this directly. For backends that
// don't, Resolution() falls back to a full-screen grab.
type Resolver interface {
	Resolution() (width, height int, err error)
}

// Resolution returns the screen resolution of sc. If sc implements Resolver
// directly, that is used. Otherwise, a full-output grab (zero rect) is tried.
func Resolution(sc Screenshotter) (int, int, error) {
	if r, ok := sc.(Resolver); ok {
		return r.Resolution()
	}
	// Fallback: grab with a zero rect — backends that support it return the
	// full output image. The image bounds reveal the output size.
	img, err := sc.Grab(image.Rect(0, 0, 0, 0))
	if err != nil {
		return 0, 0, fmt.Errorf("screen: resolution probe: %w", err)
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return 0, 0, fmt.Errorf("screen: resolution probe returned zero-size image")
	}
	return b.Dx(), b.Dy(), nil
}

// Open returns the best available Screenshotter for the current environment.
func Open() (Screenshotter, error) {
	switch compositor.Detect() {
	case compositor.KDE:
		if b, err := NewKWinShotBackend(); err == nil {
			return b, nil
		}
		if b, err := NewExtCaptureBackend(); err == nil {
			return b, nil
		}
		// Fall back to xdg-desktop-portal (xdg-desktop-portal-kde) when KWin
		// screenshot authorization is denied. The portal may show a one-time
		// consent dialog on first use; once granted the permission is remembered.
		if b, err := NewPortalDBusBackend(); err == nil {
			return b, nil
		}
		return nil, fmt.Errorf("screen: KDE requires KWin.ScreenShot2 auth or xdg-desktop-portal")

	case compositor.Wlroots:
		if b, err := NewWlrScreencopyBackend(); err == nil {
			return b, nil
		}
		if b, err := NewExtCaptureBackend(); err == nil {
			return b, nil
		}
		return nil, fmt.Errorf("screen: wlroots compositor but no screencopy protocol available")

	case compositor.GNOME:
		if b, err := NewGnomeShellScreenshotBackend(); err == nil {
			return b, nil
		}
		if b, err := NewPortalDBusBackend(); err == nil {
			return b, nil
		}
		return nil, fmt.Errorf("screen: GNOME Wayland requires GNOME Shell unsafe mode or xdg-desktop-portal")

	case compositor.X11:
		display := os.Getenv("DISPLAY")
		if display == "" {
			return nil, fmt.Errorf("screen: no display (set WAYLAND_DISPLAY or DISPLAY)")
		}
		return NewX11Backend(display)

	default: // Unknown Wayland compositor — try protocols then portal
		if b, err := NewWlrScreencopyBackend(); err == nil {
			return b, nil
		}
		if b, err := NewExtCaptureBackend(); err == nil {
			return b, nil
		}
		if b, err := NewPortalDBusBackend(); err == nil {
			return b, nil
		}
		return nil, fmt.Errorf("screen: unsupported Wayland compositor")
	}
}

// Probe returns availability details for every screen backend in priority order.
func Probe() []probe.Result {
	kind := compositor.Detect()
	globals := wl.ListGlobals(wl.SocketPath())

	return probe.SelectBest([]probe.Result{
		checkKWinShot(kind),
		checkWlrScreencopy(globals),
		checkExtCapture(globals),
		checkGnomeShellScreenshot(kind),
		checkPortalDbus(),
	})
}

func checkKWinShot(kind compositor.Session) probe.Result {
	r := probe.Result{Name: "kwin-shot2"}
	if kind != compositor.KDE {
		r.Reason = "not a KDE Plasma session"
		return r
	}
	// Try the real constructor: it performs a 1×1 probe grab so we detect
	// KDE Plasma 6 authorization failures (not just D-Bus reachability).
	b, err := NewKWinShotBackend()
	if err != nil {
		// Strip the nested "screen/kwin: authorization check failed: " prefix
		// for a cleaner one-line probe reason.
		r.Reason = "authorization denied (KDE Plasma 6 xdg permission store)"
		return r
	}
	b.Close()
	r.Available = true
	r.Reason = "org.kde.KWin on session bus"
	return r
}

func checkWlrScreencopy(globals map[string]bool) probe.Result {
	r := probe.Result{Name: "wlr-screencopy"}
	if globals == nil {
		r.Reason = "no Wayland session"
		return r
	}
	if globals["zwlr_screencopy_manager_v1"] {
		r.Available = true
		r.Reason = "zwlr_screencopy_manager_v1 advertised"
	} else {
		r.Reason = "zwlr_screencopy_manager_v1 not advertised"
	}
	return r
}

func checkExtCapture(globals map[string]bool) probe.Result {
	r := probe.Result{Name: "ext-image-copy-capture"}
	if ok, reason := extCaptureAvailable(globals); ok {
		r.Available = true
		r.Reason = reason
	} else {
		r.Reason = reason
	}
	return r
}

func checkPortalDbus() probe.Result {
	r := probe.Result{Name: "portal"}
	conn, err := dbus.SessionBus()
	if err != nil {
		r.Reason = "D-Bus session unavailable"
		return r
	}
	defer conn.Close()
	if dbusutil.HasService(conn, "org.freedesktop.portal.Desktop") {
		r.Available = true
		r.Reason = "org.freedesktop.portal.Desktop on session bus"
	} else {
		r.Reason = "org.freedesktop.portal.Desktop not on session bus"
	}
	return r
}

func checkGnomeShellScreenshot(kind compositor.Session) probe.Result {
	r := probe.Result{Name: "gnome-shell-screenshot"}
	if kind != compositor.GNOME {
		r.Reason = "not a GNOME session"
		return r
	}
	b, err := NewGnomeShellScreenshotBackend()
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "not on session bus"):
			r.Reason = "org.gnome.Shell.Screenshot not on session bus"
		default:
			r.Reason = "unsafe mode disabled or access denied"
		}
		return r
	}
	b.Close()
	r.Available = true
	r.Reason = "org.gnome.Shell.Screenshot on session bus (unsafe mode)"
	return r
}
