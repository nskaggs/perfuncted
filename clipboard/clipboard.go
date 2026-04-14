// Package clipboard provides cross-platform clipboard access for Linux desktops.
// On Wayland it uses wl-copy/wl-paste; on X11 it uses xclip. Both approaches
// spawn a subprocess, making this work regardless of compositor or toolkit.
package clipboard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Clipboard reads and writes the system clipboard.
type Clipboard interface {
	// Get returns the current clipboard text content.
	Get() (string, error)
	// Set sets the clipboard text content.
	Set(text string) error
	// Close releases any resources held by the clipboard implementation.
	Close() error
}

// Open returns the best available Clipboard for the current session.
// On Wayland sessions (WAYLAND_DISPLAY set) it uses wl-copy/wl-paste.
// On X11 sessions it uses xclip.
func Open() (Clipboard, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			if _, err := exec.LookPath("wl-paste"); err == nil {
				return &waylandClipboard{}, nil
			}
		}
	}
	if os.Getenv("DISPLAY") != "" {
		if _, err := exec.LookPath("xclip"); err == nil {
			return &x11Clipboard{}, nil
		}
	}
	return nil, fmt.Errorf("clipboard: no clipboard tool available (install wl-clipboard or xclip)")
}

// waylandClipboard uses wl-copy/wl-paste for Wayland sessions.
type waylandClipboard struct{}

func (c *waylandClipboard) Get() (string, error) {
	cmd := exec.Command("wl-paste", "--no-newline")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("clipboard: wl-paste: %w", err)
	}
	return out.String(), nil
}

func (c *waylandClipboard) Set(text string) error {
	cmd := exec.Command("wl-copy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard: wl-copy: %w", err)
	}
	return nil
}

func (c *waylandClipboard) Close() error { return nil }

// x11Clipboard uses xclip for X11 sessions.
type x11Clipboard struct{}

func (c *x11Clipboard) Get() (string, error) {
	cmd := exec.Command("xclip", "-selection", "clipboard", "-o")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("clipboard: xclip: %w", err)
	}
	return out.String(), nil
}

func (c *x11Clipboard) Set(text string) error {
	cmd := exec.Command("xclip", "-selection", "clipboard")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clipboard: xclip: %w", err)
	}
	return nil
}

func (c *x11Clipboard) Close() error { return nil }
