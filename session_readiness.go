package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func waitForFile(
	ctx context.Context,
	path string,
	attempts int,
	interval time.Duration,
) error {
	err := pollCondition(ctx, attempts, interval, func() bool {
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

func startupWaitAttempts(policy TimeoutPolicy) int {
	policy = policy.WithDefaults()
	attempts := int((policy.Startup + policy.Poll - 1) / policy.Poll)
	if attempts < 1 {
		return 1
	}
	return attempts
}

func waitForGlob(
	ctx context.Context,
	pattern string,
	attempts int,
	interval time.Duration,
) error {
	var globErr error
	err := pollCondition(ctx, attempts, interval, func() bool {
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

func pollCondition(ctx context.Context, attempts int, interval time.Duration, done func() bool) error {
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

// waitForSwayIPCSocket checks the exact managed runtime directory rather
// than globbing across its path components. File lookalikes and symlinks do
// not demonstrate that the compositor created an IPC socket.
func waitForSwayIPCSocket(ctx context.Context, dir string, attempts int, interval time.Duration) error {
	err := pollCondition(ctx, attempts, interval, func() bool {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return false
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, "sway-ipc.") && strings.HasSuffix(name, ".sock") &&
				len(name) > len("sway-ipc..sock") && entry.Type()&os.ModeSocket != 0 {
				return true
			}
		}
		return false
	})
	if errors.Is(err, errPollTimeout) {
		return fmt.Errorf("sway IPC socket in %s did not appear within %s", dir, time.Duration(attempts)*interval)
	}
	return err
}

// waitForSocket requires an actual Unix socket at the managed runtime path.
// A regular file, directory, or symlink cannot prove that the child created
// its IPC endpoint and must never be accepted as startup readiness.
func waitForSocket(ctx context.Context, path string, attempts int, interval time.Duration) error {
	err := pollCondition(ctx, attempts, interval, func() bool {
		info, statErr := os.Lstat(path)
		return statErr == nil && info.Mode()&os.ModeSocket != 0
	})
	if errors.Is(err, errPollTimeout) {
		return fmt.Errorf("socket %s did not appear within %s", path, time.Duration(attempts)*interval)
	}
	return err
}
