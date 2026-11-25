package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WaitForFile waits for the exact file path to exist.
func WaitForFile(
	ctx context.Context,
	path string,
	attempts int,
	interval time.Duration,
) error {
	err := PollCondition(ctx, attempts, interval, func() bool {
		_, statErr := os.Stat(path)
		return statErr == nil
	})
	if err == nil {
		return nil
	}
	if errors.Is(err, errPollTimeout) {
		return fmt.Errorf("%s did not appear within %s", path, time.Duration(attempts)*interval)
	}
	return err
}

// WaitForGlob waits for a valid glob to match a filesystem entry.
func WaitForGlob(
	ctx context.Context,
	pattern string,
	attempts int,
	interval time.Duration,
) error {
	var globErr error
	err := PollCondition(ctx, attempts, interval, func() bool {
		var matches []string
		matches, globErr = filepath.Glob(pattern)
		// An invalid pattern cannot become valid on a later poll.
		return globErr != nil || len(matches) > 0
	})
	if err == nil {
		if globErr != nil {
			return fmt.Errorf("invalid readiness glob %q: %w", pattern, globErr)
		}
		return nil
	}
	if errors.Is(err, errPollTimeout) {
		return fmt.Errorf("pattern %s did not match within %s", pattern, time.Duration(attempts)*interval)
	}
	return err
}

var errPollTimeout = errors.New("poll timeout")

// PollCondition observes readiness within the supplied attempt budget.
func PollCondition(ctx context.Context, attempts int, interval time.Duration, done func() bool) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready := done()
		// A readiness probe may finish just as the owner cancels startup.
		// Never accept a successful predicate after authorization has ended.
		if err := ctx.Err(); err != nil {
			return err
		}
		if ready {
			return nil
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errPollTimeout
}

// WaitForSwayIPCSocket checks the exact managed runtime directory rather
// than globbing across its path components. File lookalikes and symlinks do
// not demonstrate that the compositor created an IPC socket.
func WaitForSwayIPCSocket(ctx context.Context, dir string, attempts int, interval time.Duration) error {
	err := PollCondition(ctx, attempts, interval, func() bool { return SwaySocketPath(dir) != "" })
	if errors.Is(err, errPollTimeout) {
		return fmt.Errorf("sway IPC socket in %s did not appear within %s", dir, time.Duration(attempts)*interval)
	}
	return err
}

// WaitForSocket requires an actual Unix socket at the managed runtime path.
// A regular file, directory, or symlink cannot prove that the child created
// its IPC endpoint and must never be accepted as startup readiness.
func WaitForSocket(ctx context.Context, path string, attempts int, interval time.Duration) error {
	err := PollCondition(ctx, attempts, interval, func() bool {
		info, statErr := os.Lstat(path)
		return statErr == nil && info.Mode()&os.ModeSocket != 0
	})
	if errors.Is(err, errPollTimeout) {
		return fmt.Errorf("socket %s did not appear within %s", path, time.Duration(attempts)*interval)
	}
	return err
}

// SwaySocketPath finds an actual IPC socket within the literal managed runtime directory.
func SwaySocketPath(dir string) string {
	// xdgDir is a literal managed path. A glob would interpret brackets and
	// wildcards in its ancestors and could select another session's socket.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "sway-ipc.") || !strings.HasSuffix(name, ".sock") || len(name) <= len("sway-ipc..sock") {
			continue
		}
		// Never follow a symlink into another desktop or select a regular
		// file that merely resembles a managed Sway IPC socket.
		if entry.Type()&os.ModeSocket != 0 {
			return filepath.Join(dir, name)
		}
	}
	return ""
}
