// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

var nestedSessionGlob = filepath.Glob

// Options controls backend selection.
type Options struct {
	MaxX, MaxY int32
	Nested     bool

	XDGRuntimeDir      string
	WaylandDisplay     string
	DBusSessionAddress string
}

func resolveRuntime(opts Options) (env.Runtime, error) {
	rt := env.Current()

	xdg := opts.XDGRuntimeDir
	wayland := opts.WaylandDisplay
	dbusAddr := opts.DBusSessionAddress

	if opts.Nested {
		var err error
		xdg, wayland, dbusAddr, err = NestedEnv()
		if err != nil {
			return rt, err
		}
	}

	if xdg != "" || wayland != "" || dbusAddr != "" {
		rt = rt.WithSession(xdg, wayland, dbusAddr)
	}

	return rt, nil
}

func NestedEnv() (xdgRuntimeDir, waylandDisplay, dbusAddr string, err error) {
	matches, err := nestedSessionGlob("/tmp/perfuncted-xdg-*")
	if err != nil {
		return "", "", "", fmt.Errorf("perfuncted: glob nested sessions: %w", err)
	}
	if len(matches) == 0 {
		return "", "", "", fmt.Errorf("perfuncted: no nested session found in /tmp/perfuncted-xdg-*")
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

		fi, statErr := os.Stat(xdgDir)
		mod := time.Time{}
		if statErr == nil {
			mod = fi.ModTime()
		}
		entries = append(entries, nestedEntry{path: xdgDir, mod: mod, wl: wlSocket})
	}
	if len(entries) == 0 {
		return "", "", "", fmt.Errorf("perfuncted: no nested session found with a wayland socket in /tmp/perfuncted-xdg-*")
	}

	if len(entries) > 1 {
		// If multiple nested sessions exist, pick the most-recently-modified
		// directory among the ones that actually expose a Wayland socket.
		sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
		fmt.Fprintf(os.Stderr, "warning: multiple nested sessions found, picking %s\n", entries[0].path)
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

func DetectSession() (kind string, details map[string]string) {
	details = make(map[string]string)
	xdg := os.Getenv("XDG_RUNTIME_DIR")
	wd := os.Getenv("WAYLAND_DISPLAY")

	if strings.HasPrefix(xdg, "/tmp/perfuncted-xdg-") {
		details["dir"] = xdg
		details["wayland_display"] = wd
		details["dbus_address"] = os.Getenv("DBUS_SESSION_BUS_ADDRESS")
		return "nested", details
	}

	details["current_xdg"] = xdg
	details["current_wayland"] = wd
	return "host", details
}

// Perfuncted is the top-level session handle.
type Perfuncted struct {
	Screen    ScreenBundle
	Input     InputBundle
	Window    WindowBundle
	Clipboard ClipboardBundle
	session   compositor.Session
}

func (p *Perfuncted) Paste(text string) error {
	return p.PasteContext(context.Background(), text)
}

func (p *Perfuncted) PasteContext(ctx context.Context, text string) error {
	return p.Clipboard.PasteWithInputContext(ctx, text, p.Input)
}

// TypeFast types text quickly by using the clipboard + PasteCombo when a
// clipboard backend is available. It falls back to per-character typing via
// the Inputter Type method when the clipboard is unavailable.
func (p *Perfuncted) TypeFast(text string) error {
	return p.TypeFastContext(context.Background(), text)
}

func (p *Perfuncted) TypeFastContext(ctx context.Context, text string) error {
	if p == nil {
		return fmt.Errorf("perfuncted: nil Perfuncted")
	}
	// Prefer clipboard paste when available; this uses the session's clipboard
	// backend (wl-copy/wl-paste) and sends the paste key combo via the input
	// bundle. Falls back to Type which emits per-character key events.
	if p.Clipboard.Clipboard != nil {
		return p.Clipboard.PasteWithInputContext(ctx, text, p.Input)
	}
	return p.Input.TypeContext(ctx, text)
}

func New(opts Options) (*Perfuncted, error) {
	rt, err := resolveRuntime(opts)
	if err != nil {
		return nil, err
	}

	session := compositor.DetectRuntime(rt)

	scr, err := screen.OpenRuntime(rt)
	if err != nil {
		return nil, err
	}
	inp, err := input.OpenRuntime(rt, opts.MaxX, opts.MaxY)
	if err != nil {
		scr.Close()
		return nil, err
	}
	win, err := window.OpenRuntime(rt)
	if err != nil {
		scr.Close()
		inp.Close()
		return nil, err
	}
	cb, err := clipboard.OpenRuntime(rt)
	if err != nil {
		scr.Close()
		inp.Close()
		win.Close()
		return nil, err
	}

	return &Perfuncted{
		Screen:    ScreenBundle{scr},
		Input:     InputBundle{inp},
		Window:    WindowBundle{win},
		Clipboard: ClipboardBundle{cb},
		session:   session,
	}, nil
}

func (p *Perfuncted) Close() error {
	var errs []error
	if p.Screen.Screenshotter != nil {
		errs = append(errs, p.Screen.Close())
	}
	if p.Input.Inputter != nil {
		errs = append(errs, p.Input.Close())
	}
	if p.Window.Manager != nil {
		errs = append(errs, p.Window.Close())
	}
	if p.Clipboard.Clipboard != nil {
		errs = append(errs, p.Clipboard.Close())
	}
	return errors.Join(errs...)
}

func Retry(ctx context.Context, poll time.Duration, fn func() error) error {
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
