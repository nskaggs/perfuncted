// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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

	TraceWriter io.Writer
	TraceDelay  time.Duration

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
		slices.SortFunc(entries, func(a, b nestedEntry) int {
			if a.mod.After(b.mod) {
				return -1
			}
			if a.mod.Before(b.mod) {
				return 1
			}
			return 0
		})
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
	trace     *actionTracer
}

// Paste writes text to the clipboard and sends the paste key combo. If no
// clipboard backend is available it falls back to per-character typing.
func (p *Perfuncted) Paste(text string) error {
	if p == nil {
		return fmt.Errorf("perfuncted: nil Perfuncted")
	}
	p.traceAction(fmt.Sprintf("paste text=%q", text))
	if p.Clipboard.Clipboard != nil {
		return p.Clipboard.pasteWithInputContext(context.Background(), text, p.Input)
	}
	return p.Input.typeContext(context.Background(), text)
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
	tracer := newActionTracer(opts.TraceWriter, opts.TraceDelay)
	if tracer != nil {
		tracer.Tracef("perfuncted.new", "nested=%t max=%dx%d", opts.Nested, opts.MaxX, opts.MaxY)
	}

	return &Perfuncted{
		Screen:    ScreenBundle{Screenshotter: scr, tracer: tracer},
		Input:     InputBundle{Inputter: inp, tracer: tracer},
		Window:    WindowBundle{Manager: win, tracer: tracer},
		Clipboard: ClipboardBundle{Clipboard: cb, tracer: tracer},
		session:   session,
		trace:     tracer,
	}, nil
}

func (p *Perfuncted) Close() error {
	p.traceAction("close")
	var errs []error
	if p.Screen.Screenshotter != nil {
		errs = append(errs, p.Screen.close())
	}
	if p.Input.Inputter != nil {
		errs = append(errs, p.Input.close())
	}
	if p.Window.Manager != nil {
		errs = append(errs, p.Window.close())
	}
	if p.Clipboard.Clipboard != nil {
		errs = append(errs, p.Clipboard.close())
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

func (p *Perfuncted) traceAction(msg string) {
	if p == nil || p.trace == nil {
		return
	}
	p.trace.Tracef("perfuncted", "%s", msg)
}
