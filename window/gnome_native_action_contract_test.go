package window

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

func TestGnomeNativeActionConvertsOnlyMissingWindowFailures(t *testing.T) {
	manager := &GnomeNativeManager{bridge: &gnomebridge.Client{}}
	arbitrary := errors.New("synthetic bridge transport error")
	cases := []struct {
		name          string
		callbackError error
		want          error
	}{
		{"success", nil, nil},
		{"missing window", gnomebridge.ErrObjectNotFound, ErrWindowNotFound},
		{"wrapped missing window", fmt.Errorf("bridge: %w", gnomebridge.ErrObjectNotFound), ErrWindowNotFound},
		{"transport failure", arbitrary, arbitrary},
		{"cancellation", context.Canceled, context.Canceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			err := manager.action(nil, func(ctx context.Context) error { //nolint:staticcheck // Exercise default-context behavior explicitly.
				called++
				if ctx == nil {
					t.Fatal("action did not default nil context")
				}
				return tc.callbackError
			})
			if called != 1 {
				t.Fatalf("callback called %d times, want 1", called)
			}
			if tc.want == nil {
				if err != nil {
					t.Fatalf("successful action returned %v", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("action error %v does not preserve %v", err, tc.want)
			}
			if errors.Is(tc.want, ErrWindowNotFound) && errors.Is(err, gnomebridge.ErrObjectNotFound) {
				t.Fatalf("internal bridge error leaked to window API: %v", err)
			}
		})
	}
}

func TestGnomeNativeActionDoesNotRunAgainstUninitializedManager(t *testing.T) {
	for _, manager := range []*GnomeNativeManager{nil, {}} {
		called := false
		err := manager.action(context.Background(), func(context.Context) error {
			called = true
			return nil
		})
		if called || err == nil {
			t.Fatalf("uninitialized manager invoked callback=%t, error=%v", called, err)
		}
	}
}
