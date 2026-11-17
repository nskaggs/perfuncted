package perfuncted

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestSessionSwayConfigRejectsCancellationAfterPhaseSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var states []StartupPhaseState
	session := &Session{startupObserver: func(phase StartupPhase, state StartupPhaseState) {
		if phase != StartupPhaseSwayConfiguration {
			return
		}
		states = append(states, state)
		if state == StartupPhaseSucceeded {
			cancel()
		}
	}}

	path, err := session.resolveSessionSwayConfig(ctx, &sessionInfra{}, sessionModeNested, SessionConfig{SwayConfigPath: "/private/sway.conf"})
	if !errors.Is(err, context.Canceled) || path != "" {
		t.Fatalf("late-canceled Sway config = %q, %v; want no published path and caller cancellation", path, err)
	}
	if len(states) != 2 || states[0] != StartupPhaseStarted || states[1] != StartupPhaseSucceeded {
		t.Fatalf("Sway configuration phase states = %v, want started then succeeded before late cancellation", states)
	}
}

func TestSessionSignalCleanupReportsUnsafeRuntimeFailureAfterCancellation(t *testing.T) {
	runtimeDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir, stopDone: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	unregister := infra.CleanupOnSignal(ctx)
	cancel()
	defer unregister()

	select {
	case <-infra.stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("context cancellation did not complete session cleanup")
	}
	if infra.stopErr == nil {
		t.Fatal("cleanup failure for an unmanaged runtime was not retained")
	}
	if _, err := os.Stat(runtimeDir); err != nil {
		t.Fatalf("unsafe runtime should be preserved after failed cleanup: %v", err)
	}
}
