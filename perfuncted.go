// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
package perfuncted

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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

// DetectSession determines the session kind and environment details from the
// current process environment.
func DetectSession() (kind string, details map[string]string) {
	details = make(map[string]string)
	xdg := os.Getenv("XDG_RUNTIME_DIR")
	wd := os.Getenv("WAYLAND_DISPLAY")

	if strings.HasPrefix(xdg, nestedSessionPrefix()) {
		details["dir"] = xdg
		details["wayland_display"] = wd
		details["dbus_address"] = os.Getenv("DBUS_SESSION_BUS_ADDRESS")
		return "nested", details
	}

	details["current_xdg"] = xdg
	details["current_wayland"] = wd
	return "host", details
}

// Retry polls fn until it succeeds or ctx is cancelled. It calls fn
// immediately, then retries at the given poll interval.
func Retry(ctx context.Context, poll time.Duration, fn func() error) error {
	if fn == nil {
		return fmt.Errorf("retry: nil function")
	}
	if poll <= 0 {
		poll = 10 * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		err := fn()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("retry: timed out: %w", err)
		case <-ticker.C:
		}
	}
}
