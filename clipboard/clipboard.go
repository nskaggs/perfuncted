// Package clipboard provides cross-platform clipboard access for Linux desktops.
// On Wayland it uses wl-copy/wl-paste; on X11, it uses xclip.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/internal/wl"
)

// ErrNoClipboardTool reports that no supported clipboard executable is installed.
var ErrNoClipboardTool = errors.New("no supported clipboard tool found (install wl-clipboard or xclip)")

// Clipboard is the interface for system clipboard access.
type Clipboard interface {
	// Get returns the current clipboard text.
	Get(ctx context.Context) (string, error)
	// Set replaces the clipboard text.
	Set(ctx context.Context, text string) error
	// Close releases clipboard resources.
	Close() error
}

// Open returns a Clipboard for the current runtime environment.
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
	display := rt.Display()
	sock := rt.SocketPath()

	if compositor.DetectRuntime(rt) == compositor.GNOME {
		if clipboard, err := NewGnomeNativeClipboardForRuntime(rt); err == nil {
			return clipboard, nil
		} else if errors.Is(err, gnomebridge.ErrSessionRestartRequired) {
			return nil, err
		}
	}

	if sock != "" && wl.SocketReachable(sock) {
		if _, err := executil.LookPath("wl-copy"); err == nil {
			if _, err := executil.LookPath("wl-paste"); err == nil {
				return &extCmdClipboard{
					getCmd: []string{"wl-paste", "--no-newline"},
					// Keep the one-shot owner in the foreground. Set starts it
					// asynchronously so the subsequent physical paste request can
					// arrive without waiting on wl-copy first.
					setCmd: []string{"wl-copy", "--foreground", "--paste-once"},
					env:    extraEnv,
				}, nil
			}
		}
	}

	if display != "" {
		if _, err := executil.LookPath("xclip"); err == nil {
			return &extCmdClipboard{
				getCmd: []string{"xclip", "-selection", "clipboard", "-o"},
				setCmd: []string{"xclip", "-selection", "clipboard"},
				env:    extraEnv,
			}, nil
		}
	}

	return nil, ErrNoClipboardTool
}

type extCmdClipboard struct {
	getCmd []string
	setCmd []string
	env    []string
}

func (c *extCmdClipboard) Get(ctx context.Context) (string, error) {
	ctx = contextutil.Default(ctx)
	cmd := executil.CommandContext(ctx, c.getCmd[0], c.getCmd[1:]...)
	// Ensure the external tool runs with the session env captured at Open().
	cmd.Env = c.env

	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("clipboard get: %w", ctx.Err())
		}
		return "", fmt.Errorf("clipboard get: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}

func (c *extCmdClipboard) Set(ctx context.Context, text string) error {
	ctx = contextutil.Default(ctx)
	if filepath.Base(c.setCmd[0]) == "wl-copy" {
		return c.setWayland(ctx, text)
	}
	cmd := executil.CommandContext(ctx, c.setCmd[0], c.setCmd[1:]...)
	// Ensure the external tool runs with the session env captured at Open().
	cmd.Env = c.env

	cmd.Stdin = bytes.NewBufferString(text)
	var stderr bytes.Buffer
	var stderrFile *os.File
	var stderrPath string
	tool := filepath.Base(c.setCmd[0])
	if tool == "wl-copy" || tool == "xclip" {
		// Both clipboard owners can leave a child process holding stderr open
		// after the command that populated the selection exits. A regular file
		// preserves diagnostics without making os/exec wait on that child.
		var err error
		stderrFile, err = os.CreateTemp("", "perfuncted-clipboard-stderr-*")
		if err != nil {
			return fmt.Errorf("clipboard set: create stderr capture: %w", err)
		}
		stderrPath = stderrFile.Name()
		defer func() {
			_ = stderrFile.Close()
			_ = os.Remove(stderrPath)
		}()
		cmd.Stderr = stderrFile
	} else {
		cmd.Stderr = &stderr
	}

	runErr := cmd.Run()
	if stderrFile != nil {
		closeErr := stderrFile.Close()
		if closeErr == nil {
			if data, readErr := os.ReadFile(stderrPath); readErr == nil {
				_, _ = stderr.Write(data)
			} else if runErr == nil {
				return fmt.Errorf("clipboard set: read stderr capture: %w", readErr)
			}
		} else if runErr == nil {
			return fmt.Errorf("clipboard set: close stderr capture: %w", closeErr)
		}
	}

	if runErr != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("clipboard set: %w", ctx.Err())
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return fmt.Errorf("clipboard set: %w", runErr)
		}
		return fmt.Errorf("clipboard set: %w: %s", runErr, message)
	}
	return nil
}

// setWayland starts a one-shot clipboard owner and returns before it waits for
// the consumer. wl-copy cannot exit until a paste request arrives, while the
// caller cannot send that request until Set returns. The owner is bounded and
// reaped in the background so a failed paste cannot leave an unbounded child.
func (c *extCmdClipboard) setWayland(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("clipboard set: %w", err)
	}
	input, err := os.CreateTemp("", "perfuncted-clipboard-input-*")
	if err != nil {
		return fmt.Errorf("clipboard set: create input: %w", err)
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	if _, writeErr := input.WriteString(text); writeErr != nil {
		_ = input.Close()
		return fmt.Errorf("clipboard set: write input: %w", writeErr)
	}
	if _, seekErr := input.Seek(0, 0); seekErr != nil {
		_ = input.Close()
		return fmt.Errorf("clipboard set: rewind input: %w", seekErr)
	}

	cmd := executil.CommandContext(context.WithoutCancel(ctx), c.setCmd[0], c.setCmd[1:]...)
	cmd.Env = c.env
	cmd.Stdin = input
	stderrFile, err := os.CreateTemp("", "perfuncted-clipboard-stderr-*")
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("clipboard set: create stderr capture: %w", err)
	}
	stderrPath := stderrFile.Name()
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = stderrFile.Close()
		_ = os.Remove(stderrPath)
		return fmt.Errorf("clipboard set: %w", err)
	}
	_ = input.Close()

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		_ = stderrFile.Close()
		_ = os.Remove(stderrPath)
		close(done)
	}()

	ownerTimeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < ownerTimeout {
			ownerTimeout = remaining
		}
	}
	if ownerTimeout <= 0 {
		_ = cmd.Process.Kill()
		return fmt.Errorf("clipboard set: %w", ctx.Err())
	}
	go func() {
		timer := time.NewTimer(ownerTimeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	return nil
}

func (c *extCmdClipboard) Close() error { return nil }

func captureRuntimeEnv(rt env.Runtime) []string {
	return rt.EnvList()
}
