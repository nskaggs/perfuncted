// Package clipboard provides cross-platform clipboard access for Linux desktops.
// On Wayland it uses wl-copy/wl-paste; on X11, it uses xclip.
package clipboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/internal/wl"
	pollpkg "github.com/nskaggs/perfuncted/poll"
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
	return OpenRuntimeContext(context.Background(), rt)
}

// OpenRuntimeContext opens the clipboard backend for rt while honoring ctx
// during context-aware GNOME bridge setup.
func OpenRuntimeContext(ctx context.Context, rt env.Runtime) (Clipboard, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Capture current session-specific environment so external clipboard
	// tools (wl-copy/wl-paste) are invoked against the correct Wayland
	// compositor when the parent process later calls Set/Get.
	extraEnv := captureRuntimeEnv(rt)
	display := rt.Display()
	sock := rt.SocketPath()

	if compositor.DetectRuntime(rt) == compositor.GNOME {
		if clipboard, err := NewGnomeNativeClipboardForRuntimeContext(ctx, rt); err == nil {
			return clipboard, nil
		} else if errors.Is(err, gnomebridge.ErrSessionRestartRequired) {
			return nil, err
		} else if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
	}

	if sock != "" && wl.SocketReachable(sock) {
		if _, err := executil.LookPath("wl-copy"); err == nil {
			if _, err := executil.LookPath("wl-paste"); err == nil {
				return &extCmdClipboard{
					getCmd: []string{"wl-paste", "--no-newline"},
					// Keep the owner in the foreground. Set starts it asynchronously
					// so multiple consumers can read the selection without waiting on
					// wl-copy first.
					setCmd: []string{"wl-copy", "--foreground"},
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

	if err := ctx.Err(); err != nil {
		return nil, err
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

// setWayland starts a foreground clipboard owner and returns after the
// compositor advertises the selection. wl-copy cannot exit until its selection
// is replaced or the process is stopped, while the caller cannot send the
// request until Set returns. The owner is bounded and reaped in the background
// so a failed paste cannot leave an unbounded child.
func (c *extCmdClipboard) setWayland(ctx context.Context, text string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("clipboard set: %w", err)
	}
	input, inputPath, err := waylandInput(text)
	if err != nil {
		return err
	}
	defer os.Remove(inputPath)
	cmd, done, err := c.startWaylandOwner(ctx, input)
	if err != nil {
		_ = input.Close()
		return err
	}
	_ = input.Close()

	// Session-bound calls carry their policy deadline. Direct callers without
	// one retain a finite readiness fallback.
	readyCtx, readyCancel := waylandReadinessContext(ctx)
	readyErr := c.waitWaylandContent(readyCtx, text)
	readyCancel()
	if readyErr != nil {
		stopWaylandOwner(cmd, done)
		if ctx.Err() != nil {
			return fmt.Errorf("clipboard set: %w", ctx.Err())
		}
		return fmt.Errorf("clipboard set: owner readiness: %w", readyErr)
	}

	// The owner must remain alive long enough for readers to consume the
	// selection, but its lifetime is bounded independently from the caller's
	// readiness operation. A session deadline still caps this duration.
	ownerTimeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < ownerTimeout {
			ownerTimeout = remaining
		}
	}
	if ownerTimeout <= 0 {
		stopWaylandOwner(cmd, done)
		return fmt.Errorf("clipboard set: %w", ctx.Err())
	}
	go func() {
		timer := time.NewTimer(ownerTimeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			stopWaylandOwner(cmd, done)
		}
	}()
	return nil
}

func waylandReadinessContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return contextutil.WithTimeoutFallback(ctx, time.Second)
}

func waylandInput(text string) (*os.File, string, error) {
	input, err := os.CreateTemp("", "perfuncted-clipboard-input-*")
	if err != nil {
		return nil, "", fmt.Errorf("clipboard set: create input: %w", err)
	}
	inputPath := input.Name()
	if _, err := input.WriteString(text); err != nil {
		_ = input.Close()
		_ = os.Remove(inputPath)
		return nil, "", fmt.Errorf("clipboard set: write input: %w", err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		_ = input.Close()
		_ = os.Remove(inputPath)
		return nil, "", fmt.Errorf("clipboard set: rewind input: %w", err)
	}
	return input, inputPath, nil
}

func (c *extCmdClipboard) startWaylandOwner(ctx context.Context, input *os.File) (*exec.Cmd, <-chan struct{}, error) {
	cmd := executil.CommandContext(context.WithoutCancel(ctx), c.setCmd[0], c.setCmd[1:]...)
	cmd.Env = c.env
	cmd.Stdin = input
	stderrFile, err := os.CreateTemp("", "perfuncted-clipboard-stderr-*")
	if err != nil {
		return nil, nil, fmt.Errorf("clipboard set: create stderr capture: %w", err)
	}
	stderrPath := stderrFile.Name()
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		_ = stderrFile.Close()
		_ = os.Remove(stderrPath)
		return nil, nil, fmt.Errorf("clipboard set: %w", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		_ = stderrFile.Close()
		_ = os.Remove(stderrPath)
		close(done)
	}()
	return cmd, done, nil
}

func stopWaylandOwner(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	// Reaping is best-effort cleanup after the caller has already requested a
	// stop; this short grace is deliberately independent of operation policy.
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

const (
	// waylandPollMinInterval and waylandPollMaxInterval bound how often the
	// compositor is asked for the new selection. Every read forks wl-paste, so
	// the interval is the fork rate: asking more often than the compositor can
	// answer only spends process creation.
	waylandPollMinInterval = 10 * time.Millisecond
	waylandPollMaxInterval = 250 * time.Millisecond
)

// waylandContentMatches reports whether the compositor already serves want.
func (c *extCmdClipboard) waylandContentMatches(ctx context.Context, want string) bool {
	cmd := executil.CommandContext(ctx, c.getCmd[0], c.getCmd[1:]...)
	cmd.Env = c.env
	var out bytes.Buffer
	cmd.Stdout = &out
	return cmd.Run() == nil && out.String() == want
}

// waitWaylandContent waits until the compositor serves want.
//
// Clipboard ownership has no readiness event in this external-tool protocol.
// The owner is started with --foreground precisely so it keeps running while it
// holds the selection, which means its exit is a death signal rather than
// evidence that the new content is being served. Observation of the compositor
// is therefore polled, and the interval backs off because each read forks
// wl-paste: the compositor needs the same order of time to answer no matter how
// often it is asked, so a fixed short interval converts a slow compositor into
// thousands of process creations inside one readiness budget.
func (c *extCmdClipboard) waitWaylandContent(ctx context.Context, want string) error {
	if len(c.getCmd) == 0 {
		return nil
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if c.waylandContentMatches(ctx, want) {
			return nil
		}
		timer := time.NewTimer(pollpkg.AdaptivePoll(attempt, waylandPollMinInterval, waylandPollMaxInterval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *extCmdClipboard) Close() error { return nil }

func captureRuntimeEnv(rt env.Runtime) []string {
	return rt.EnvList()
}
