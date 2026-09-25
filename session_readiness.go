package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	err := pollCondition(ctx, attempts, interval, func() bool {
		matches, globErr := filepath.Glob(pattern)
		return globErr == nil && len(matches) > 0
	})
	if err == nil {
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
		if done() {
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
