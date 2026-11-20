package perfuncted

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The scanner may finish successfully at the exact instant the caller revokes
// authorization. The final check must reject that scan and release its retry
// throttle, rather than reporting cleanup success.
type cancelOnSecondTopLevelCleanupCheck struct {
	context.Context
	cancel context.CancelCauseFunc
	calls  int
}

func (c *cancelOnSecondTopLevelCleanupCheck) Err() error {
	pc, _, _, ok := runtime.Caller(1)
	if ok {
		fn := runtime.FuncForPC(pc)
		if fn != nil && strings.HasSuffix(fn.Name(), ".CleanupStaleSessionsContext") {
			c.calls++
			if c.calls == 2 {
				c.cancel(errors.New("revoked after scan"))
			}
		}
	}
	return c.Context.Err()
}

func TestStaleSessionCleanupRejectsCancellationAfterSuccessfulScan(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cleanupStaleSessionsMu.Lock()
	priorTime, priorAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = priorTime, priorAttempt
		cleanupStaleSessionsMu.Unlock()
	})
	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelOnSecondTopLevelCleanupCheck{Context: base, cancel: cancel}
	err := CleanupStaleSessionsContext(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) || ctx.calls != 2 {
		t.Fatalf("cleanup completed after final revocation: err=%v top-level checks=%d", err, ctx.calls)
	}
	cleanupStaleSessionsMu.Lock()
	released := lastCleanupTime.IsZero()
	cleanupStaleSessionsMu.Unlock()
	if !released {
		t.Fatal("failed cleanup retained stale-scan throttle")
	}
}
