package perfuncted

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStartSessionReportsUnavailableSwayAndCleansManagedRuntime(t *testing.T) {
	root := prepareHeadlessHelpers(t, true)
	bin := filepath.Join(root, "bin")
	if err := os.Remove(filepath.Join(bin, "sway")); err != nil {
		t.Fatalf("remove synthetic Sway executable: %v", err)
	}

	var events []startupEvent
	pathRestricted := false
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	session.startupObserver = func(phase StartupPhase, state StartupPhaseState) {
		events = append(events, startupEvent{phase: phase, state: state})
		if phase == StartupPhaseDBusSocketReady && state == StartupPhaseSucceeded && !pathRestricted {
			// D-Bus is already running from the synthetic helper. Restrict the
			// subsequent compositor lookup to this directory, which has no Sway.
			t.Setenv("PATH", bin)
			pathRestricted = true
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{
		Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond},
	}, false)
	if infra != nil {
		t.Fatal("failed Sway startup returned live session infrastructure")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("startSession error = %v, want missing Sway executable", err)
	}
	if !pathRestricted {
		t.Fatal("test did not restrict Sway lookup after D-Bus readiness")
	}
	for _, want := range []startupEvent{
		{StartupPhaseSwayProcessSpawn, StartupPhaseStarted},
		{StartupPhaseSwayProcessSpawn, StartupPhaseFailed},
		{StartupPhaseSwayStartup, StartupPhaseFailed},
	} {
		if !hasStartupEvent(events, want) {
			t.Fatalf("failed startup omitted %+v from events %+v", want, events)
		}
	}
	for _, event := range events {
		if event.phase == StartupPhaseSwaySocketsReady {
			t.Fatalf("startup entered socket readiness without a Sway process: %+v", events)
		}
	}
	requireNoManagedSessionDirs(t, root)
}
