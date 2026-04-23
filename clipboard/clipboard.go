// Package clipboard provides cross-platform clipboard access for Linux desktops.
// On Wayland it uses wl-copy/wl-paste; on X11, it uses xclip.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/internal/executil"
)

var ErrNoClipboardTool = errors.New("no supported clipboard tool found (install wl-clipboard or xclip)")

// Clipboard is the interface for system clipboard access.
type Clipboard interface {
	Get(ctx context.Context) (string, error)
	Set(ctx context.Context, text string) error
	Close() error
}

// Bundle wraps a Clipboard with a check function.
type Bundle struct {
	Clipboard
}

func (b Bundle) checkAvailable() error {
	if b.Clipboard == nil {
		return fmt.Errorf("clipboard: not available")
	}
	return nil
}

func (b Bundle) Get(ctx context.Context) (string, error) {
	if err := b.checkAvailable(); err != nil {
		return "", err
	}
	return b.Clipboard.Get(ctx)
}

func (b Bundle) Set(ctx context.Context, text string) error {
	if err := b.checkAvailable(); err != nil {
		return err
	}
	return b.Clipboard.Set(ctx, text)
}

// Open detects the environment and returns the appropriate Clipboard backend.
func Open() (Bundle, error) {
	// Capture current session-specific environment so external clipboard
	// tools (wl-copy/wl-paste) are invoked against the correct Wayland
	// compositor when the parent process later calls Set/Get.
	var extraEnv []string
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		extraEnv = append(extraEnv, "XDG_RUNTIME_DIR="+x)
	}
	if w := os.Getenv("WAYLAND_DISPLAY"); w != "" {
		extraEnv = append(extraEnv, "WAYLAND_DISPLAY="+w)
	}
	if d := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); d != "" {
		extraEnv = append(extraEnv, "DBUS_SESSION_BUS_ADDRESS="+d)
	}

	// Wayland check
	if _, err := executil.LookPath("wl-copy"); err == nil {
		if _, err := executil.LookPath("wl-paste"); err == nil {
			return Bundle{&extCmdClipboard{
				getCmd: []string{"wl-paste", "--no-newline"},
				setCmd: []string{"wl-copy"},
				env:    extraEnv,
			}}, nil
		}
	}
	// X11 check
	if _, err := executil.LookPath("xclip"); err == nil {
		return Bundle{&extCmdClipboard{
			getCmd: []string{"xclip", "-selection", "clipboard", "-o"},
			setCmd: []string{"xclip", "-selection", "clipboard"},
			env:    extraEnv,
		}}, nil
	}
	return Bundle{}, ErrNoClipboardTool
}

type extCmdClipboard struct {
	getCmd []string
	setCmd []string
	env    []string
}

func (c *extCmdClipboard) Get(ctx context.Context) (string, error) {
	cmd := executil.CommandContext(ctx, c.getCmd[0], c.getCmd[1:]...)
	// Ensure the external tool runs with the session env captured at Open().
	cmd.Env = executil.MergeEnv(c.env, os.Environ())
	// Log the env values used so tests can verify we're targeting the headless session.
	var xdg, wl, dbus string
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			xdg = strings.TrimPrefix(e, "XDG_RUNTIME_DIR=")
		}
		if strings.HasPrefix(e, "WAYLAND_DISPLAY=") {
			wl = strings.TrimPrefix(e, "WAYLAND_DISPLAY=")
		}
		if strings.HasPrefix(e, "DBUS_SESSION_BUS_ADDRESS=") {
			dbus = strings.TrimPrefix(e, "DBUS_SESSION_BUS_ADDRESS=")
		}
	}
	fmt.Printf("DEBUG: running %v (clipboard get) with XDG_RUNTIME_DIR=%q WAYLAND_DISPLAY=%q DBUS_SESSION_BUS_ADDRESS=%q\n", c.getCmd, xdg, wl, dbus)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("clipboard get: %w", err)
	}
	return out.String(), nil
}

func (c *extCmdClipboard) Set(ctx context.Context, text string) error {
	// Retry transient failures from external clipboard tools a few times to
	// improve robustness in CI where wl-copy can occasionally fail.
	var lastErr error
	for i := 0; i < 3; i++ {
		cmd := executil.CommandContext(ctx, c.setCmd[0], c.setCmd[1:]...)
		// Ensure the external tool runs with the session env captured at Open().
		cmd.Env = executil.MergeEnv(c.env, os.Environ())
		// Log the env values used so tests can verify we're targeting the headless session.
		var xdg, wl, dbus string
		for _, e := range cmd.Env {
			if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
				xdg = strings.TrimPrefix(e, "XDG_RUNTIME_DIR=")
			}
			if strings.HasPrefix(e, "WAYLAND_DISPLAY=") {
				wl = strings.TrimPrefix(e, "WAYLAND_DISPLAY=")
			}
			if strings.HasPrefix(e, "DBUS_SESSION_BUS_ADDRESS=") {
				dbus = strings.TrimPrefix(e, "DBUS_SESSION_BUS_ADDRESS=")
			}
		}
		fmt.Printf("DEBUG: running %v (clipboard set) with XDG_RUNTIME_DIR=%q WAYLAND_DISPLAY=%q DBUS_SESSION_BUS_ADDRESS=%q\n", c.setCmd, xdg, wl, dbus)
		cmd.Stdin = bytes.NewBufferString(text)
		if err := cmd.Run(); err != nil {
			lastErr = err
			// If context was cancelled, return immediately.
			select {
			case <-ctx.Done():
				return fmt.Errorf("clipboard set: %w", ctx.Err())
			default:
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return nil
	}
	return fmt.Errorf("clipboard set: %w", lastErr)
}

func (c *extCmdClipboard) Close() error { return nil }
