package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Cancel on the first check made directly by a particular cleanup phase.
// The decision stays independent of extra entries in a shared /tmp root.
type cancelAtCleanupPhaseContext struct {
	context.Context
	cancel    context.CancelCauseFunc
	phase     string
	triggered bool
}

func (c *cancelAtCleanupPhaseContext) Err() error {
	if !c.triggered {
		pc, _, _, ok := runtime.Caller(1)
		if ok {
			fn := runtime.FuncForPC(pc)
			if fn != nil && strings.HasSuffix(fn.Name(), "."+c.phase) {
				c.triggered = true
				c.cancel(errors.New("caller revoked cleanup before phase dispatch"))
			}
		}
	}
	return c.Context.Err()
}

// Discovery is not permission to reclaim any of its results. Even when a
// managed directory has been identified, cancellation before the first reap
// check or during metadata/grace inspection must preserve the filesystem.
func TestStaleSessionScanPreservesDiscoveredRuntimeAfterCallerRevocation(t *testing.T) {
	for _, phase := range []string{"scanStaleSessions", "reapSessionDirWhenOlderThan"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TMPDIR", root)
			runtimeDir := filepath.Join(root, "perfuncted-xdg-private")
			if err := os.Mkdir(runtimeDir, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(runtimeDir, "private-marker")
			if err := os.WriteFile(marker, []byte("retain"), 0600); err != nil {
				t.Fatal(err)
			}
			owner, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			ctx := &cancelAtCleanupPhaseContext{Context: owner, cancel: cancel, phase: phase}
			err := scanStaleSessions(ctx, time.Hour)
			if !ctx.triggered || !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(owner)) {
				t.Fatalf("phase %s ignored caller revocation: triggered=%v err=%v", phase, ctx.triggered, err)
			}
			got, readErr := os.ReadFile(marker)
			if readErr != nil || string(got) != "retain" {
				t.Fatalf("phase %s deleted private runtime after cancellation: data=%q err=%v", phase, got, readErr)
			}
		})
	}
}
