// Package clipboard provides cross-platform clipboard access for Linux desktops.
// On Wayland it uses wl-copy/wl-paste; on X11, it uses xclip.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
)

var ErrNoClipboardTool = errors.New("no supported clipboard tool found (install wl-clipboard or xclip)")

// Clipboard is the interface for system clipboard access.
type Clipboard interface {
	Get(ctx context.Context) (string, error)
	Set(ctx context.Context, text string) error
	Close() error
}

// Open detects the environment and returns the appropriate Clipboard backend.
func Open() (Clipboard, error) {
	return OpenRuntime(env.Current())
}

// OpenRuntime detects the environment represented by rt and returns the
// appropriate Clipboard backend.
func OpenRuntime(rt env.Runtime) (Clipboard, error) {
	// Capture current session-specific environment so external clipboard
	// tools (wl-copy/wl-paste) are invoked against the correct Wayland
	// compositor when the parent process later calls Set/Get.
	extraEnv := captureRuntimeEnv(rt)

	// Wayland check
	if _, err := executil.LookPath("wl-copy"); err == nil {
		if _, err := executil.LookPath("wl-paste"); err == nil {
			return &extCmdClipboard{
				getCmd: []string{"wl-paste", "--no-newline"},
				setCmd: []string{"wl-copy"},
				env:    extraEnv,
			}, nil
		}
	}
	// X11 check
	if _, err := executil.LookPath("xclip"); err == nil {
		return &extCmdClipboard{
			getCmd: []string{"xclip", "-selection", "clipboard", "-o"},
			setCmd: []string{"xclip", "-selection", "clipboard"},
			env:    extraEnv,
		}, nil
	}
	return nil, ErrNoClipboardTool
}

type extCmdClipboard struct {
	getCmd []string
	setCmd []string
	env    []string
}

func (c *extCmdClipboard) Get(ctx context.Context) (string, error) {
	cmd := executil.CommandContext(ctx, c.getCmd[0], c.getCmd[1:]...)
	// Ensure the external tool runs with the session env captured at Open().
	cmd.Env = c.env

	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("clipboard get: %w", err)
	}
	// Normalize trailing-newline differences between backends by trimming a
	// single trailing '\n'. This keeps behaviour consistent for callers.
	return strings.TrimRight(out.String(), "\n"), nil
}

func (c *extCmdClipboard) Set(ctx context.Context, text string) error {
	cmd := executil.CommandContext(ctx, c.setCmd[0], c.setCmd[1:]...)
	// Ensure the external tool runs with the session env captured at Open().
	cmd.Env = c.env

	cmd.Stdin = bytes.NewBufferString(text)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("clipboard set: %w", ctx.Err())
		}
		return fmt.Errorf("clipboard set: %w", err)
	}
	return nil
}

func (c *extCmdClipboard) Close() error { return nil }

func captureRuntimeEnv(rt env.Runtime) []string {
	return rt.EnvList()
}
