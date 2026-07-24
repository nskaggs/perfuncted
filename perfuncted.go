// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
package perfuncted

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

var nestedSessionGlob = filepath.Glob

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

// NestedEnv locates a running nested session and returns its XDG runtime
// directory, Wayland display, and D-Bus address.
func NestedEnv() (xdgRuntimeDir, waylandDisplay, dbusAddr string, err error) {
	pattern := nestedSessionPattern()
	matches, err := nestedSessionGlob(pattern)
	if err != nil {
		return "", "", "", fmt.Errorf("perfuncted: glob nested sessions: %w", err)
	}
	if len(matches) == 0 {
		return "", "", "", fmt.Errorf("perfuncted: no nested session found in %s", pattern)
	}
	type nestedEntry struct {
		path string
		mod  time.Time
		wl   string
	}

	var entries []nestedEntry
	for _, xdgDir := range matches {
		wlSocket, socketErr := nestedWaylandSocket(xdgDir)
		if socketErr != nil {
			continue
		}
		if !nestedSessionPIDAlive(xdgDir) {
			continue
		}

		fi, statErr := os.Stat(xdgDir)
		mod := time.Time{}
		if statErr == nil {
			mod = fi.ModTime()
		}
		entries = append(entries, nestedEntry{path: xdgDir, mod: mod, wl: wlSocket})
	}
	if len(entries) == 0 {
		return "", "", "", fmt.Errorf("perfuncted: no nested session found with a wayland socket in %s", pattern)
	}

	if len(entries) > 1 {
		slices.SortFunc(entries, func(a, b nestedEntry) int {
			if a.mod.After(b.mod) {
				return -1
			}
			if a.mod.Before(b.mod) {
				return 1
			}
			return 0
		})
	}

	xdgDir := entries[0].path
	return xdgDir, entries[0].wl, fmt.Sprintf("unix:path=%s/bus", xdgDir), nil
}

func nestedWaylandSocket(xdgDir string) (string, error) {
	sockets, err := filepath.Glob(filepath.Join(xdgDir, "wayland-*"))
	if err != nil {
		return "", fmt.Errorf("perfuncted: glob wayland sockets: %w", err)
	}
	for _, sock := range sockets {
		if strings.HasSuffix(sock, ".lock") {
			continue
		}
		return filepath.Base(sock), nil
	}
	return "", fmt.Errorf("perfuncted: no wayland socket in %s", xdgDir)
}

func nestedSessionPIDAlive(xdgDir string) bool {
	data, err := os.ReadFile(filepath.Join(xdgDir, "perfuncted.pid"))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	return pidAlive(pid)
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
