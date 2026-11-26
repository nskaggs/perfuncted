// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
package perfuncted

import (
	sessionruntime "github.com/nskaggs/perfuncted/v2/internal/runtime"

	"os"

	"github.com/nskaggs/perfuncted/v2/clipboard"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
)

// ErrGNOMESessionRestartRequired reports that perfuncted installed or replaced
// its bundled GNOME Shell extension and GNOME must load it at the next login.
// It is safe to inspect with errors.Is.
var ErrGNOMESessionRestartRequired = gnomebridge.ErrSessionRestartRequired

// Injectable backend constructors for testing.
var (
	openScreen             = screen.OpenRuntimeContext
	openInput              = input.OpenRuntimeContext
	openRemoteDesktopInput = input.OpenRemoteDesktopContext
	openWindow             = window.OpenRuntimeContext
	openOutput             = output.OpenRuntimeContext
	openClipboard          = clipboard.OpenRuntimeContext
)

// SessionDetection is an immutable snapshot of the environment used to
// classify the current desktop session.
type SessionDetection struct {
	// Kind is the detected host or nested session classification.
	Kind TargetKind
	// XDGRuntimeDir is the detected XDG runtime directory.
	XDGRuntimeDir string
	// WaylandDisplay is the detected Wayland display name, if any.
	WaylandDisplay string
	// DBusAddress is the detected session bus address, if any.
	DBusAddress string
}

// DetectSession determines the current session kind and returns typed
// environment fields. It does not open a backend or claim ownership of the
// detected session.
func DetectSession() SessionDetection {
	xdg := os.Getenv("XDG_RUNTIME_DIR")
	detection := SessionDetection{
		Kind:           TargetHost,
		XDGRuntimeDir:  xdg,
		WaylandDisplay: os.Getenv("WAYLAND_DISPLAY"),
		DBusAddress:    os.Getenv("DBUS_SESSION_BUS_ADDRESS"),
	}
	if sessionruntime.IsManagedSessionDir(xdg) {
		detection.Kind = TargetNested
	}
	return detection
}
