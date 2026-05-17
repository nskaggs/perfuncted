// Package perfuncted is a Go library for automating Linux desktop applications.
// It auto-detects the right backend at runtime across X11, wlroots Wayland
// (Sway, Hyprland), KDE Plasma, and GNOME — no configuration needed.
package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/clipboard"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

var nestedSessionGlob = filepath.Glob

// Options controls backend selection.
type Options struct {
	MaxX, MaxY int32
	Nested     bool

	TraceWriter io.Writer
	TraceLogger *slog.Logger
	TraceDelay  time.Duration

	XDGRuntimeDir      string
	WaylandDisplay     string
	DBusSessionAddress string

	// ManagedSession is stopped and cleaned when Perfuncted.Close is called.
	// It is set by Session.Perfuncted for callers that want one handle to own
	// both API bundles and the underlying isolated session lifecycle.
	ManagedSession *Session
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

func nestedSessionPattern() string {
	return filepath.Join(os.TempDir(), "perfuncted-xdg-*")
}

func nestedSessionPrefix() string {
	return filepath.Join(os.TempDir(), "perfuncted-xdg-")
}

// Perfuncted is the top-level session handle.
type Perfuncted struct {
	Screen    ScreenBundle
	Input     InputBundle
	Window    WindowBundle
	Output    OutputBundle
	Clipboard ClipboardBundle
	session   compositor.Session
	managed   *Session
	trace     *actionTracer
}

// Paste writes text to the clipboard and sends the paste key combo. If no
// clipboard backend is available it falls back to per-character typing.
func (p *Perfuncted) Paste(ctx context.Context, text string) error {
	if p == nil {
		return fmt.Errorf("perfuncted: nil Perfuncted")
	}
	ctx = normalizeContext(ctx)
	p.traceAction(fmt.Sprintf("paste text=%q", text))
	if p.Clipboard.Clipboard != nil {
		return p.Clipboard.pasteWithInputContext(ctx, text, p.Input)
	}
	return p.Input.typeContext(ctx, text)
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
	out, err := output.OpenRuntime(rt)
	if err != nil {
		scr.Close()
		inp.Close()
		win.Close()
		return nil, err
	}
	cb, err := clipboard.OpenRuntime(rt)
	if err != nil {
		scr.Close()
		inp.Close()
		win.Close()
		out.Close()
		return nil, err
	}
	tracer := newActionTracer(opts.TraceWriter, opts.TraceLogger, opts.TraceDelay)
	if tracer != nil {
		tracer.Tracef("perfuncted.new", "nested=%t max=%dx%d", opts.Nested, opts.MaxX, opts.MaxY)
	}

	return &Perfuncted{
		Screen:    ScreenBundle{Screenshotter: scr, tracer: tracer},
		Input:     InputBundle{Inputter: inp, tracer: tracer},
		Window:    WindowBundle{Manager: win, tracer: tracer},
		Output:    OutputBundle{Lister: out, tracer: tracer},
		Clipboard: ClipboardBundle{Clipboard: cb, tracer: tracer},
		session:   session,
		managed:   opts.ManagedSession,
		trace:     tracer,
	}, nil
}

func (p *Perfuncted) Close() error {
	if p == nil {
		return nil
	}
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
	if p.Output.Lister != nil {
		errs = append(errs, p.Output.close())
	}
	if p.Clipboard.Clipboard != nil {
		errs = append(errs, p.Clipboard.close())
	}
	if p.managed != nil {
		p.managed.Cleanup()
	}
	return errors.Join(errs...)
}

func Retry(ctx context.Context, poll time.Duration, fn func() error) error {
	ctx = normalizeContext(ctx)
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

func (p *Perfuncted) traceAction(msg string) {
	if p == nil || p.trace == nil {
		return
	}
	p.trace.Tracef("perfuncted", "%s", msg)
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
