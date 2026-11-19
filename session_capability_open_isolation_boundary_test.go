package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
)

// Opening one unavailable capability must not initialize or alter another
// capability. All openers are synthetic: no host desktop is contacted.
func TestCapabilityOpenWindowAndOutputFailuresRemainIsolated(t *testing.T) {
	preserveOpeners(t)
	failure := errors.New("synthetic desktop backend unavailable")
	var windowCalls, outputCalls int
	openWindow = func(context.Context, env.Runtime) (window.Manager, error) {
		windowCalls++
		return nil, failure
	}
	openOutput = func(context.Context, env.Runtime) (output.Lister, error) {
		outputCalls++
		return nil, failure
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	for _, cap := range []Capability{CapabilityWindows, CapabilityOutputs} {
		backend, err := session.openCapabilityContext(context.Background(), cap)
		if backend != nil || !errors.Is(err, failure) {
			t.Fatalf("failed capability %s exposed backend=%v err=%v", cap, backend, err)
		}
	}
	if windowCalls != 1 || outputCalls != 1 {
		t.Fatalf("unexpected opener calls: windows=%d outputs=%d", windowCalls, outputCalls)
	}
	if session.Windows.backend != nil || session.Outputs.backend != nil {
		t.Fatal("failed capability opener mutated unrelated facade backend")
	}
}

// When no managed resolution is configured, keyboard backends must receive
// unconstrained axes rather than inheriting a host display's dimensions.
func TestCapabilityOpenInputWithoutResolutionUsesZeroBounds(t *testing.T) {
	preserveOpeners(t)
	failure := errors.New("synthetic input device unavailable")
	called := false
	openInput = func(_ context.Context, _ env.Runtime, maxX, maxY int32) (input.Inputter, error) {
		called = true
		if maxX != 0 || maxY != 0 {
			t.Errorf("missing managed resolution inherited bounds %dx%d", maxX, maxY)
		}
		return nil, failure
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	backend, err := session.openCapabilityContext(context.Background(), CapabilityInput)
	if !called || backend != nil || !errors.Is(err, failure) {
		t.Fatalf("unconstrained input opener was not rejected: called=%v backend=%v err=%v", called, backend, err)
	}
}
