// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
package perfuncted

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// Injectable backend constructors for testing.
var (
	openScreen    = screen.OpenRuntime
	openInput     = input.OpenRuntime
	openWindow    = window.OpenRuntime
	openOutput    = output.OpenRuntime
	openClipboard = clipboard.OpenRuntime
)

func nestedSessionPattern() string {
	return filepath.Join(os.TempDir(), "perfuncted-xdg-*")
}

func nestedSessionPrefix() string {
	return filepath.Join(os.TempDir(), "perfuncted-xdg-")
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// SessionDetection is an immutable snapshot of the environment used to
// classify the current desktop session. It replaces the historical
// string-plus-map DetectSession result, whose keys and mutability were not a
// stable contract.
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
	if strings.HasPrefix(xdg, nestedSessionPrefix()) {
		detection.Kind = TargetNested
	}
	return detection
}
