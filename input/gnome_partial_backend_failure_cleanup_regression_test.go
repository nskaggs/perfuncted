//go:build linux

package input

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

// An unsuccessful GNOME constructor may already have acquired resources.
// Falling back, or requiring a session restart, must release those resources.
func TestOpenRuntimeClosesPartialGnomeBackendBeforeFailureOrFallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cause    error
		fallback bool
	}{
		{"ordinary-bridge-error", errors.New("GNOME bridge failed"), true},
		{"session-restart-required", gnomebridge.ErrSessionRestartRequired, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRuntimeSelectorConstructors(t)
			partial := &selectorTrackedInputter{}
			selected := &selectorTrackedInputter{}
			gnomeCalls, virtualCalls := 0, 0
			newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) { gnomeCalls++; return partial, tc.cause }
			newWlVirtualBackend = func(context.Context, string) (Inputter, error) { virtualCalls++; return selected, nil }
			got, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
			if partial.closed != 1 || gnomeCalls != 1 {
				t.Fatalf("failed GNOME backend leaked or closed twice: closes=%d calls=%d err=%v", partial.closed, gnomeCalls, err)
			}
			if tc.fallback {
				if err != nil || got != selected || virtualCalls != 1 {
					t.Fatalf("fallback missing after cleanup: backend=%T err=%v calls=%d", got, err, virtualCalls)
				}
			} else if got != nil || !errors.Is(err, tc.cause) || virtualCalls != 0 {
				t.Fatalf("terminal bridge failure ignored: backend=%T err=%v virtualCalls=%d", got, err, virtualCalls)
			}
		})
	}
}
