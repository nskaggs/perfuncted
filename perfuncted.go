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
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// RemoteDesktopInputOptions configures the explicitly authorized Wayland
// RemoteDesktop and EIS input backend.
type RemoteDesktopInputOptions = input.RemoteDesktopOptions

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

func nestedSessionPattern() string {
	return filepath.Join(os.TempDir(), "perfuncted-xdg-*")
}

func nestedSessionPrefix() string {
	return filepath.Join(os.TempDir(), "perfuncted-xdg-")
}

// matchesManagedSessionRoot accepts only a directly nested managed runtime
// directory. Never broaden /tmp cleanup to arbitrary caller-owned paths.
func matchesManagedSessionRoot(dir, root string) bool {
	prefix := filepath.Join(root, "perfuncted-xdg-")
	if !strings.HasPrefix(dir, prefix) || len(dir) <= len(prefix) {
		return false
	}
	return !strings.Contains(dir[len(prefix):], string(os.PathSeparator))
}

func isSafeToRemoveDir(dir string) bool {
	if dir == "" {
		return false
	}
	cleaned := filepath.Clean(dir)
	if cleaned == "/" || cleaned == "." {
		return false
	}
	tmp := filepath.Clean(os.TempDir())
	if cleaned == tmp {
		return false
	}
	if strings.HasPrefix(cleaned, tmp+string(os.PathSeparator)) {
		return true
	}
	// Only our exact managed pattern qualifies outside the caller's TMPDIR.
	return matchesManagedSessionRoot(cleaned, "/tmp")
}

func isManagedSessionDir(dir string) bool {
	if !isSafeToRemoveDir(dir) {
		return false
	}
	cleaned := filepath.Clean(dir)
	for _, root := range managedSessionTempRoots() {
		if matchesManagedSessionRoot(cleaned, root) {
			return true
		}
	}
	return false
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

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
	if isManagedSessionDir(xdg) {
		detection.Kind = TargetNested
	}
	return detection
}
