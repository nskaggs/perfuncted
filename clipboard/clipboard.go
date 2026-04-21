// Package clipboard provides cross-platform clipboard access for Linux desktops.
// On Wayland it uses wl-copy/wl-paste; on X11, it uses xclip.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"

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
	// Wayland check
	if _, err := executil.LookPath("wl-copy"); err == nil {
		if _, err := executil.LookPath("wl-paste"); err == nil {
			return Bundle{&extCmdClipboard{
				getCmd: []string{"wl-paste", "--no-newline"},
				setCmd: []string{"wl-copy"},
			}}, nil
		}
	}
	// X11 check
	if _, err := executil.LookPath("xclip"); err == nil {
		return Bundle{&extCmdClipboard{
			getCmd: []string{"xclip", "-selection", "clipboard", "-o"},
			setCmd: []string{"xclip", "-selection", "clipboard"},
		}}, nil
	}
	return Bundle{}, ErrNoClipboardTool
}

type extCmdClipboard struct {
	getCmd []string
	setCmd []string
}

func (c *extCmdClipboard) Get(ctx context.Context) (string, error) {
	cmd := executil.CommandContext(ctx, c.getCmd[0], c.getCmd[1:]...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("clipboard get: %w", err)
	}
	return out.String(), nil
}

func (c *extCmdClipboard) Set(ctx context.Context, text string) error {
	cmd := executil.CommandContext(ctx, c.setCmd[0], c.setCmd[1:]...)
	cmd.Stdin = bytes.NewBufferString(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard set: %w", err)
	}
	return nil
}

func (c *extCmdClipboard) Close() error { return nil }
