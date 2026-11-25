package runtime

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Malformed readiness patterns are configuration errors, not missing sockets.
// Retrying them hides the cause and can needlessly consume startup time.
func TestWaitForGlobReturnsInvalidPatternInsteadOfTimeout(t *testing.T) {
	err := WaitForGlob(context.Background(), "[unterminated", 2, time.Millisecond)
	if !errors.Is(err, filepath.ErrBadPattern) {
		t.Fatalf("malformed readiness glob returned %v; want ErrBadPattern", err)
	}
}

// Actual startup readiness must distinguish a delayed resource, a permanent
// absence, and an explicit cancellation without changing its retry budget.
func TestManagedReadinessFileAndGlobContract(t *testing.T) {
	root := t.TempDir()
	present := filepath.Join(root, "ready.socket")
	if err := os.WriteFile(present, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name           string
		wait           func(context.Context) error
		timeoutContext string
	}{
		{"file present", func(ctx context.Context) error { return WaitForFile(ctx, present, 1, time.Millisecond) }, ""},
		{"glob present", func(ctx context.Context) error {
			return WaitForGlob(ctx, filepath.Join(root, "*.socket"), 1, time.Millisecond)
		}, ""},
		{"file absent", func(ctx context.Context) error {
			return WaitForFile(ctx, filepath.Join(root, "absent"), 1, time.Millisecond)
		}, "did not appear"},
		{"glob absent", func(ctx context.Context) error {
			return WaitForGlob(ctx, filepath.Join(root, "*.missing"), 1, time.Millisecond)
		}, "did not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.wait(context.Background())
			if tc.timeoutContext == "" && err != nil {
				t.Fatalf("ready resource refused: %v", err)
			}
			if tc.timeoutContext != "" && (err == nil || !strings.Contains(err.Error(), tc.timeoutContext)) {
				t.Fatalf("timeout not classified: got %v, wanted %q", err, tc.timeoutContext)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForFile(ctx, present, 2, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("file readiness ignored cancellation: %v", err)
	}
	if err := WaitForGlob(ctx, filepath.Join(root, "*.socket"), 2, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("glob readiness ignored cancellation: %v", err)
	}
}

// Polling a denied or absent resource must not exceed the declared number of
// attempts, even when no polling operation is requested.
func TestReadinessProbeBudgetAndCancellation(t *testing.T) {
	for _, attempts := range []int{0, 1, 3} {
		probes := 0
		err := PollCondition(context.Background(), attempts, time.Millisecond, func() bool {
			probes++
			return false
		})
		if !errors.Is(err, errPollTimeout) || probes != attempts {
			t.Fatalf("attempts=%d, probes=%d, err=%v", attempts, probes, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	probes := 0
	err := PollCondition(ctx, 2, time.Millisecond, func() bool {
		probes++
		cancel()
		return false
	})
	if !errors.Is(err, context.Canceled) || probes != 1 {
		t.Fatalf("canceled probe continued: probes=%d, err=%v", probes, err)
	}
}

// Readiness is not authorization to continue a canceled startup: cancellation
// may arrive while a filesystem or compositor predicate is executing.
func TestPollConditionRejectsCancellationDuringSuccessfulProbe(t *testing.T) {
	for _, reportsReady := range []bool{true, false} {
		t.Run(map[bool]string{true: "ready", false: "not-ready"}[reportsReady], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probes := 0
			err := PollCondition(ctx, 1, time.Millisecond, func() bool {
				probes++
				cancel()
				return reportsReady
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled readiness probe returned %v; want cancellation", err)
			}
			if probes != 1 {
				t.Fatalf("unexpected readiness probes: %d", probes)
			}
		})
	}
}

// A deleted or unavailable managed runtime must not satisfy compositor
// readiness, even if other Sway sockets are available on the host.
func TestSwayReadinessMissingManagedRuntimeFailsClosed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	err := WaitForSwayIPCSocket(context.Background(), missing, 1, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "sway IPC socket") {
		t.Fatalf("missing managed runtime unexpectedly ready: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := WaitForSwayIPCSocket(ctx, missing, 3, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled readiness must report caller cancellation: %v", err)
	}
}

func TestManagedSwayIPCReadinessUsesLiteralRuntimeAndRealSocket(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfready-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	own := filepath.Join(root, "private[unfinished")
	if err := os.Mkdir(own, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(own, "sway-ipc.1.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := WaitForSwayIPCSocket(context.Background(), own, 1, time.Millisecond); err != nil {
		t.Fatalf("real socket under literal runtime not ready: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, []byte("fake"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WaitForSwayIPCSocket(context.Background(), own, 1, time.Millisecond); err == nil {
		t.Fatal("non-socket file passed Sway IPC readiness")
	}
}
