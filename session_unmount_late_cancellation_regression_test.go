package perfuncted

import (
	"context"
	"errors"
	"testing"
)

// A mount query may return successfully at the same instant a caller cancels.
// An empty reply is not permission to report completion after cancellation.
func TestManagedMountCleanupNeverAcknowledgesCancelledMountScan(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-unmount"
	for _, tc := range []struct {
		name         string
		cancelOnRead int
	}{
		{"initial scan", 1},
		{"post-unmount verification", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			unmounts := 0
			err := unmountSubdirsWith(ctx, root,
				func(_ string) ([]string, error) {
					reads++
					if reads == tc.cancelOnRead {
						cancel()
					}
					if reads == 1 && tc.cancelOnRead == 2 {
						return []string{root + "/private-mount"}, nil
					}
					return nil, nil
				},
				func(context.Context, string) error { unmounts++; return nil },
			)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled mount scan acknowledged success: reads=%d unmounts=%d err=%v", reads, unmounts, err)
			}
			if reads != tc.cancelOnRead {
				t.Fatalf("incorrect scan count: got=%d want=%d", reads, tc.cancelOnRead)
			}
			if tc.cancelOnRead == 1 && unmounts != 0 {
				t.Fatalf("initial canceled scan performed %d unmounts", unmounts)
			}
			if tc.cancelOnRead == 2 && unmounts != 1 {
				t.Fatalf("fixture did not reach verification after one unmount: %d", unmounts)
			}
		})
	}
}
