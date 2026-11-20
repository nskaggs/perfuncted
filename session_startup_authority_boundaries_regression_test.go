package perfuncted

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/screen"
)

type cancelingScreenDiagnostics struct {
	capabilityScreen
	cancel context.CancelFunc
}

func (b *cancelingScreenDiagnostics) Diagnostics() []string {
	b.cancel()
	return nil
}

type cancelingAccessibilityDiagnostics struct {
	*bundleAccessibilityFake
	cancel context.CancelFunc
	closes atomic.Int32
}

func (b *cancelingAccessibilityDiagnostics) Diagnostics() []string {
	b.cancel()
	return nil
}

func (b *cancelingAccessibilityDiagnostics) Close() error {
	b.closes.Add(1)
	return nil
}

// Cancellation while collecting backend metadata must not publish a session,
// whether further capabilities remain or the last capability has just opened.
func TestOpenCancellationDuringCapabilityDiagnosticsClosesBackend(t *testing.T) {
	t.Run("before next capability", func(t *testing.T) {
		preserveOpeners(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		backend := &cancelingScreenDiagnostics{cancel: cancel}
		openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
			return backend, nil
		}
		session, err := Open(ctx, Require(CapabilityScreen))
		if session != nil || !errors.Is(err, context.Canceled) || backend.closeCalls.Load() != 1 {
			t.Fatalf("Open = (%v, %v), backend closes=%d", session, err, backend.closeCalls.Load())
		}
	})
	t.Run("after last capability", func(t *testing.T) {
		previous := openAccessibility
		t.Cleanup(func() { openAccessibility = previous })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		backend := &cancelingAccessibilityDiagnostics{bundleAccessibilityFake: &bundleAccessibilityFake{}, cancel: cancel}
		openAccessibility = func(context.Context, env.Runtime) (accessibility.Backend, error) {
			return backend, nil
		}
		session, err := Open(ctx, Require(CapabilityAccessibility))
		if session != nil || !errors.Is(err, context.Canceled) || backend.closes.Load() != 1 {
			t.Fatalf("Open = (%v, %v), backend closes=%d", session, err, backend.closes.Load())
		}
	})
}

func TestNestedStartupCanceledDuringStaleCleanupDoesNotStartRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	managedStarted := false
	session, err := Open(ctx,
		WithNested(SessionConfig{Timeouts: TimeoutPolicy{Startup: time.Second}}),
		WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
			if phase == StartupPhaseStaleSessionCleanup && state == StartupPhaseStarted {
				cancel()
			}
			if phase == StartupPhaseManagedRuntimeSetup {
				managedStarted = true
			}
		}),
	)
	if session != nil || !errors.Is(err, context.Canceled) || managedStarted {
		t.Fatalf("nested startup = (%v, %v), managed runtime started=%v", session, err, managedStarted)
	}
}
