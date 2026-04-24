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
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

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
	matches, err := filepath.Glob("/tmp/perfuncted-xdg-*")
	if err != nil {
		return "", "", "", fmt.Errorf("perfuncted: glob nested sessions: %w", err)
	}
	if len(matches) == 0 {
		return "", "", "", fmt.Errorf("perfuncted: no nested session found in /tmp/perfuncted-xdg-*")
	}
	if len(matches) > 1 {
		// If multiple nested sessions exist, pick the most-recently-modified
		// directory to be robust in CI where parallel runs may leave multiple
		// entries. Emit a warning to stderr to help debugging.
		type mtimeEntry struct {
			path string
			mod  time.Time
		}
		var entries []mtimeEntry
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil {
				entries = append(entries, mtimeEntry{path: m, mod: fi.ModTime()})
			} else {
				entries = append(entries, mtimeEntry{path: m, mod: time.Time{}})
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
		xdgDir := entries[0].path
		fmt.Fprintf(os.Stderr, "warning: multiple nested sessions found, picking %s\n", xdgDir)
		matches = []string{xdgDir}
	}

	xdgDir := matches[0]
	sockets, err := filepath.Glob(filepath.Join(xdgDir, "wayland-*"))
	if err != nil {
		return "", "", "", fmt.Errorf("perfuncted: glob wayland sockets: %w", err)
	}
	var wlSocket string
	for _, sock := range sockets {
		if !strings.HasSuffix(sock, ".lock") {
			wlSocket = filepath.Base(sock)
			break
		}
	}
	if wlSocket == "" {
		return "", "", "", fmt.Errorf("perfuncted: no wayland socket in %s", xdgDir)
	}

	return xdgDir, wlSocket, fmt.Sprintf("unix:path=%s/bus", xdgDir), nil
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
}

func (p *Perfuncted) Paste(text string) error {
	return p.PasteContext(context.Background(), text)
}

func (p *Perfuncted) PasteContext(ctx context.Context, text string) error {
	return p.Clipboard.PasteWithInputContext(ctx, text, p.Input)
}

func New(opts Options) (*Perfuncted, error) {
	rt, err := resolveRuntime(opts)
	if err != nil {
		return nil, err
	}

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
	}, nil
}

func (p *Perfuncted) Close() error {
	var errs []error
	if p.Screen.Screenshotter != nil {
		errs = append(errs, p.Screen.Screenshotter.Close())
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
	for {
		err := fn()
		if err == nil {
			return nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("retry: timed out: %w", err)
		case <-timer.C:
		}
	}
}
