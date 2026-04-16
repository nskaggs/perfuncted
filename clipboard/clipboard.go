// Package clipboard provides cross-platform clipboard access for Linux desktops.
// On Wayland it uses wl-copy/wl-paste; on X11 it uses xclip. Both approaches
// spawn a subprocess, making this work regardless of compositor or toolkit.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
)

const commandTimeout = 5 * time.Second

// ErrNoClipboardTool is returned when no supported clipboard helper is present.
var ErrNoClipboardTool = errors.New("clipboard: no clipboard tool available (install wl-clipboard or xclip)")

// Clipboard reads and writes the system clipboard.
type Clipboard interface {
	// Get returns the current clipboard text content.
	Get() (string, error)
	// Set sets the clipboard text content.
	Set(text string) error
	// Close releases any resources held by the clipboard implementation.
	Close() error
}

// extCmdClipboard is a thin implementation backed by external commands.
type extCmdClipboard struct {
	getCmd []string
	setCmd []string
	env    []string
}

func (c *extCmdClipboard) Get() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := executil.CommandContext(ctx, c.getCmd[0], c.getCmd[1:]...)
	cmd.Env = c.env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("clipboard: %s: %w", c.getCmd[0], err)
	}
	return out.String(), nil
}

func (c *extCmdClipboard) Set(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := executil.CommandContext(ctx, c.setCmd[0], c.setCmd[1:]...)
	cmd.Env = c.env
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard: %s: %w", c.setCmd[0], err)
	}
	return nil
}

func (c *extCmdClipboard) Close() error { return nil }

// Open returns the best available Clipboard for the current session.
// On Wayland sessions (WAYLAND_DISPLAY set) it uses wl-copy/wl-paste.
// On X11 sessions it uses xclip.
func Open() (Clipboard, error) {
	env := env.Merge(os.Environ())
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if copyPath, err := executil.LookPath("wl-copy"); err == nil {
			if pastePath, err := executil.LookPath("wl-paste"); err == nil {
				return &extCmdClipboard{getCmd: []string{pastePath, "--no-newline"}, setCmd: []string{copyPath}, env: env}, nil
			}
		}
	}
	if os.Getenv("DISPLAY") != "" {
		if xclipPath, err := executil.LookPath("xclip"); err == nil {
			return &extCmdClipboard{getCmd: []string{xclipPath, "-selection", "clipboard", "-o"}, setCmd: []string{xclipPath, "-selection", "clipboard"}, env: env}, nil
		}
	}
	return nil, ErrNoClipboardTool
}
