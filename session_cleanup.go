package perfuncted

import (
	"context"
	"fmt"
	"sync"
	"time"

	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"

	"github.com/nskaggs/perfuncted/internal/contextutil"
)

var (
	cleanupStaleSessionsMu sync.Mutex
	lastCleanupTime        time.Time
	cleanupAttempt         uint64
)

const (
	sessionOwnerPIDFile     = sessionruntime.OwnerPIDFile
	cleanupStaleMinInterval = 30 * time.Second
)

// CleanupStaleSessions removes perfuncted session directories immediately when
// their recorded parent PID is no longer running. Missing ownership metadata
// gets a five-minute creation grace; malformed metadata uses maxAge, clamped
// to that same minimum.
func CleanupStaleSessions(maxAge time.Duration) {
	_ = CleanupStaleSessionsContext(context.Background(), maxAge)
}

// CleanupStaleSessionsContext removes stale managed sessions while honoring
// cancellation during process shutdown and mount cleanup. Remaining directories
// are safe to retry on a later cleanup pass.
func CleanupStaleSessionsContext(ctx context.Context, maxAge time.Duration) error {
	if ctx == nil {
		return fmt.Errorf("session cleanup: %w: nil context", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return contextutil.ErrorWithCause(ctx)
	}
	// Claim the interval while locked, then scan outside it so concurrent Opens
	// don't wait for process termination. A canceled newest attempt releases its
	// claim without clearing a newer scan's rate limit.
	cleanupStaleSessionsMu.Lock()
	if time.Since(lastCleanupTime) < cleanupStaleMinInterval {
		cleanupStaleSessionsMu.Unlock()
		if err := ctx.Err(); err != nil {
			return contextutil.ErrorWithCause(ctx)
		}
		return nil
	}
	cleanupAttempt++
	attempt := cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()

	err := sessionruntime.ScanStaleSessions(ctx, maxAge)
	if err == nil && ctx.Err() != nil {
		err = contextutil.ErrorWithCause(ctx)
	}
	if err != nil {
		cleanupStaleSessionsMu.Lock()
		if cleanupAttempt == attempt {
			lastCleanupTime = time.Time{}
		}
		cleanupStaleSessionsMu.Unlock()
	}
	return err
}
