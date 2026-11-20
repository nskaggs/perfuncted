package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedStartupCleansUpWhenSwayConfigCannotBePublished(t *testing.T) {
	root := prepareHeadlessHelpers(t, true)
	var setupErr error
	var events []startupEvent
	session := &Session{startupObserver: func(phase StartupPhase, state StartupPhaseState) {
		events = append(events, startupEvent{phase: phase, state: state})
		if phase != StartupPhaseSwayConfiguration || state != StartupPhaseStarted {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			setupErr = err
			return
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
				setupErr = os.Mkdir(filepath.Join(root, entry.Name(), "sway.conf"), 0700)
				return
			}
		}
		setupErr = errors.New("managed runtime not found")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{
		Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond},
	}, false)
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	if infra != nil || err == nil || !strings.Contains(err.Error(), "write config") {
		t.Fatalf("startup with conflicting config destination = (%v, %v)", infra, err)
	}
	if !hasStartupEvent(events, startupEvent{StartupPhaseSwayConfiguration, StartupPhaseFailed}) {
		t.Fatalf("missing configuration failure event: %+v", events)
	}
	for _, event := range events {
		if event.phase == StartupPhaseSwayProcessSpawn {
			t.Fatalf("Sway started after config failure: %+v", events)
		}
	}
	requireNoManagedSessionDirs(t, root)
}
