// Package screen provides screen capture backends for X11 and Wayland.
package screen

import (
	"fmt"
	"image"
	"os"

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

// Open returns the best available Screenshotter for the current environment.
func Open() (Screenshotter, error) {
	switch compositor.Detect() {
	case compositor.KDE:
		if b, err := NewKWinShotBackend(); err == nil {
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
		if b, err := NewPortalDBusBackend(); err == nil {
			return b, nil
		}
		return nil, fmt.Errorf("screen: GNOME Wayland requires xdg-desktop-portal")

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
	if globals == nil {
		r.Reason = "no Wayland session"
		return r
	}
	if globals["ext_image_copy_capture_manager_v1"] {
		r.Available = true
		r.Reason = "ext_image_copy_capture_manager_v1 advertised"
	} else {
		r.Reason = "ext_image_copy_capture_manager_v1 not advertised"
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
	if dbusutil.HasService(conn, "org.freedesktop.portal.Desktop") {
		r.Available = true
		r.Reason = "org.freedesktop.portal.Desktop on session bus"
	} else {
		r.Reason = "org.freedesktop.portal.Desktop not on session bus"
	}
	return r
}
