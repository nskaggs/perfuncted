// Package compositor detects which Wayland compositor (or X11 session) is
// running so that screen and window backends can select the right implementation
// without trial-and-error probing.
//go:build linux
// +build linux

package compositor

import (
	"strings"

	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// Session describes the detected runtime session environment.
type Session int

const (
	X11     Session = iota // no WAYLAND_DISPLAY; pure X11 or XWayland outer session
	KDE                    // KDE Plasma Wayland — use KWin D-Bus APIs
	Wlroots                // wlroots compositor: sway, Hyprland, river, Wayfire
	GNOME                  // GNOME Wayland — most automation APIs unavailable
	Unknown                // Wayland session, compositor unrecognised
)

func (s Session) String() string {
	switch s {
	case X11:
		return "X11"
	case KDE:
		return "KDE Plasma Wayland"
	case Wlroots:
		return "wlroots Wayland"
	case GNOME:
		return "GNOME Wayland"
	default:
		return "unknown Wayland"
	}
}

// Detect identifies the current compositor by probing the actual globals
// advertised on WAYLAND_DISPLAY (correctly handles nested compositors such as
// sway inside KDE), then falls back to environment variable heuristics.
func Detect() Session {
	return DetectRuntime(env.Current())
}

// DetectRuntime identifies the compositor represented by rt.
func DetectRuntime(rt env.Runtime) Session {
	if rt.Get("WAYLAND_DISPLAY") == "" {
		return X11
	}

	// Probe the real compositor socket first.
	if s, ok := probeGlobals(rt); ok {
		return s
	}

	// Env-var fallbacks (fast but unreliable for nested sessions).
	if rt.Get("SWAYSOCK") != "" || rt.Get("HYPRLAND_INSTANCE_SIGNATURE") != "" {
		return Wlroots
	}
	desktop := strings.ToUpper(rt.Get("XDG_CURRENT_DESKTOP"))
	switch {
	case strings.Contains(desktop, "KDE"):
		return KDE
	case strings.Contains(desktop, "GNOME"):
		return GNOME
	case strings.Contains(desktop, "SWAY"),
		strings.Contains(desktop, "HYPRLAND"),
		strings.Contains(desktop, "RIVER"),
		strings.Contains(desktop, "WAYFIRE"):
		return Wlroots
	}
	if kwinOnBus(rt.Get("DBUS_SESSION_BUS_ADDRESS")) {
		return KDE
	}
	return Unknown
}

// probeGlobals connects to WAYLAND_DISPLAY and inspects the advertised
// interface names to determine the compositor family.
// Wlroots protocols seen on the actual socket take priority over KDE D-Bus
// presence, so nested sway/Hyprland sessions inside a KDE desktop are
// correctly identified as wlroots.
func probeGlobals(rt env.Runtime) (Session, bool) {
	sock := rt.SocketPath()
	if sock == "" {
		return 0, false
	}
	// Use the Session helper to connect and synchronously collect globals.
	s, err := wl.NewSession(sock)
	if err != nil {
		return 0, false
	}
	defer s.Close()

	var hasWlroots, hasKDE, hasGNOME bool
	for _, ev := range s.Globals {
		iface := ev.Interface
		switch {
		case iface == "zwlr_screencopy_manager_v1" ||
			iface == "zwlr_foreign_toplevel_manager_v1" ||
			iface == "zwlr_virtual_keyboard_manager_v1" ||
			iface == "zwlr_virtual_pointer_manager_v1":
			hasWlroots = true
		case strings.HasPrefix(iface, "org_kde_") || strings.HasPrefix(iface, "kde_"):
			hasKDE = true
		case iface == "gtk_shell1" || iface == "gtk_surface1":
			hasGNOME = true
		}
	}

	// Return priority based on collected globals.
	switch {
	case hasWlroots:
		return Wlroots, true
	case hasKDE:
		return KDE, true
	case hasGNOME:
		return GNOME, true
	default:
		return Unknown, true
	}
}

func kwinOnBus(addr string) bool {
	conn, err := dbusutil.SessionBusAddress(addr)
	if err != nil {
		return false
	}
	defer conn.Close()
	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return false
	}
	for _, n := range names {
		if strings.HasPrefix(n, "org.kde.KWin") {
			return true
		}
	}
	return false
}
