package perfuncted

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A failed first mount scan leaves ownership of mounted subtrees unknown.
// Cleanup must fail before invoking any unmount helper, even if a partial
// listing accompanied the inspection failure.
func TestManagedUnmountRefusesIncompleteInitialMountInspection(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-unverified"
	inspectErr := errors.New("incomplete mount namespace snapshot")
	for _, tc := range []struct {
		name   string
		mounts []string
	}{
		{"no mount listing", nil},
		{"partial mount listing", []string{root + "/private"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, unmounts := 0, 0
			err := unmountSubdirsWith(context.Background(), root, func(dir string) ([]string, error) {
				if dir != root {
					t.Fatalf("inspected wrong directory: %q", dir)
				}
				reads++
				return tc.mounts, inspectErr
			}, func(context.Context, string) error { unmounts++; return nil })
			if !errors.Is(err, inspectErr) || !strings.Contains(err.Error(), "inspect mounts before cleanup") || reads != 1 || unmounts != 0 {
				t.Fatalf("unsafe cleanup after incomplete mount scan: err=%v reads=%d unmounts=%d", err, reads, unmounts)
			}
		})
	}
}

// Cancellation and a simultaneous mount-table error are independent reasons
// cleanup must stop. Preserve both so callers can distinguish cancellation
// from the inspection failure, and do not mutate any mount.
func TestManagedUnmountPreservesCancellationAndInitialInspectionFailure(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-canceled-mount-inspection"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inspectErr := errors.New("mountinfo became unavailable")
	unmountCalls := 0
	err := unmountSubdirsWith(ctx, root, func(string) ([]string, error) {
		cancel()
		return []string{root + "/subtree"}, inspectErr
	}, func(context.Context, string) error { unmountCalls++; return nil })
	if !errors.Is(err, context.Canceled) || !errors.Is(err, inspectErr) || unmountCalls != 0 {
		t.Fatalf("canceled failed inspection lost causes or mutated mount: err=%v unmounts=%d", err, unmountCalls)
	}
}
