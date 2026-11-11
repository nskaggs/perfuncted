package perfuncted

import (
	"context"
	"errors"
	"testing"
)

func TestUnmountCleanupRetainsConcurrentCancellationAndMountFailure(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-cancel-error"
	cases := []struct {
		name  string
		stage string
	}{
		{"initial mount scan", "initial"},
		{"unmount helper", "unmount"},
		{"verification scan", "verify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("synthetic mount subsystem error")
			reads, unmounts := 0, 0
			err := unmountSubdirsWith(ctx, root, func(string) ([]string, error) {
				reads++
				if tc.stage == "initial" {
					cancel()
					return nil, failure
				}
				if reads == 1 {
					return []string{root + "/nested"}, nil
				}
				if tc.stage == "verify" {
					cancel()
					return nil, failure
				}
				return nil, nil
			}, func(context.Context, string) error {
				unmounts++
				if tc.stage == "unmount" {
					cancel()
					return failure
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || !errors.Is(err, failure) {
				t.Fatalf("lost cancellation or underlying mount error: %v", err)
			}
			if tc.stage == "initial" && unmounts != 0 {
				t.Fatalf("unmounted after failed initial scan: %d", unmounts)
			}
		})
	}
}
