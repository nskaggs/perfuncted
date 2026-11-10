package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
		{"file present", func(ctx context.Context) error { return waitForFile(ctx, present, 1, time.Millisecond) }, ""},
		{"glob present", func(ctx context.Context) error {
			return waitForGlob(ctx, filepath.Join(root, "*.socket"), 1, time.Millisecond)
		}, ""},
		{"file absent", func(ctx context.Context) error {
			return waitForFile(ctx, filepath.Join(root, "absent"), 1, time.Millisecond)
		}, "did not appear"},
		{"glob absent", func(ctx context.Context) error {
			return waitForGlob(ctx, filepath.Join(root, "*.missing"), 1, time.Millisecond)
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
	if err := waitForFile(ctx, present, 2, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("file readiness ignored cancellation: %v", err)
	}
	if err := waitForGlob(ctx, filepath.Join(root, "*.socket"), 2, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("glob readiness ignored cancellation: %v", err)
	}
}

// Polling a denied or absent resource must not exceed the declared number of
// attempts, even when no polling operation is requested.
func TestReadinessProbeBudgetAndCancellation(t *testing.T) {
	for _, attempts := range []int{0, 1, 3} {
		probes := 0
		err := pollCondition(context.Background(), attempts, time.Millisecond, func() bool {
			probes++
			return false
		})
		if !errors.Is(err, errPollTimeout) || probes != attempts {
			t.Fatalf("attempts=%d, probes=%d, err=%v", attempts, probes, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	probes := 0
	err := pollCondition(ctx, 2, time.Millisecond, func() bool {
		probes++
		cancel()
		return false
	})
	if !errors.Is(err, context.Canceled) || probes != 1 {
		t.Fatalf("canceled probe continued: probes=%d, err=%v", probes, err)
	}
}
