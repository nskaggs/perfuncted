package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStaleSessionCleanupNilContextAndThrottledCancellation(t *testing.T) {
	err := CleanupStaleSessionsContext(nil, time.Minute) //nolint:staticcheck // Explicit nil-context validation is under test.
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil cleanup context returned %v", err)
	}
	cleanupStaleSessionsMu.Lock()
	savedTime, savedAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = savedTime, savedAttempt
		cleanupStaleSessionsMu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CleanupStaleSessionsContext(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("throttled canceled cleanup returned %v", err)
	}
}

func TestManagedUnmountCancellationAndNoMountBoundaries(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-safe"
	for _, tc := range []struct {
		name                            string
		cancelAt                        string
		expectedReads, expectedUnmounts int
	}{
		{"pre-canceled", "before", 0, 0},
		{"after initial inspection", "after read", 1, 0},
		{"after first unmount", "first unmount", 1, 1},
		{"after failed unmount", "failed unmount", 1, 1},
		{"after verification", "verify", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelAt == "before" {
				cancel()
			}
			reads, unmounts := 0, 0
			err := unmountSubdirsWith(ctx, root, func(string) ([]string, error) {
				reads++
				if tc.cancelAt == "after read" && reads == 1 || tc.cancelAt == "verify" && reads == 2 {
					cancel()
				}
				if reads == 1 {
					return []string{root + "/one", root + "/two"}, nil
				}
				return nil, nil
			}, func(context.Context, string) error {
				unmounts++
				if tc.cancelAt == "first unmount" || tc.cancelAt == "failed unmount" {
					cancel()
				}
				if tc.cancelAt == "failed unmount" {
					return errors.New("synthetic unmount failed")
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || reads != tc.expectedReads || unmounts != tc.expectedUnmounts {
				t.Fatalf("cancellation result: err=%v scans=%d/%d unmounts=%d/%d", err, reads, tc.expectedReads, unmounts, tc.expectedUnmounts)
			}
		})
	}
}

func TestManagedUnmountEmptyOrNoMountScans(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-safe"
	readCalled := false
	if err := unmountSubdirsWith(context.Background(), "", func(string) ([]string, error) { readCalled = true; return nil, nil }, func(context.Context, string) error { t.Fatal("unexpected unmount"); return nil }); err != nil || readCalled {
		t.Fatalf("empty runtime inspected mounts=%v error=%v", readCalled, err)
	}
	readCalled = false
	if err := unmountSubdirsWith(context.Background(), root, func(string) ([]string, error) { readCalled = true; return nil, nil }, func(context.Context, string) error { t.Fatal("unexpected unmount"); return nil }); err != nil || !readCalled {
		t.Fatalf("empty mount list: read=%v error=%v", readCalled, err)
	}
}

func TestStaleSessionReapCanceledBeforeInspectingOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reapSessionDir(ctx, "/synthetic/perfuncted-xdg-cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled reaping result: %v", err)
	}
	if err := reapSessionDirWhenOlderThan(ctx, "/synthetic/perfuncted-xdg-cancelled", time.Now(), time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled age check result: %v", err)
	}
}
