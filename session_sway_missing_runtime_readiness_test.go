package perfuncted

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A deleted or unavailable managed runtime must not satisfy compositor
// readiness, even if other Sway sockets are available on the host.
func TestSwayReadinessMissingManagedRuntimeFailsClosed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	err := waitForSwayIPCSocket(context.Background(), missing, 1, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "sway IPC socket") {
		t.Fatalf("missing managed runtime unexpectedly ready: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForSwayIPCSocket(ctx, missing, 3, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled readiness must report caller cancellation: %v", err)
	}
}
