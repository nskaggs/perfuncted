package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStartSessionCancellationAfterSwayReadinessStopsProcessesAndCleansRuntime(t *testing.T) {
	root := prepareHeadlessHelpers(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runtimeDir string
	var processIDs []int
	var observerErr error
	var events []startupEvent
	session := &Session{startupObserver: func(phase StartupPhase, state StartupPhaseState) {
		events = append(events, startupEvent{phase: phase, state: state})
		if phase != StartupPhaseSwaySocketsReady || state != StartupPhaseSucceeded {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			observerErr = fmt.Errorf("inspect synthetic runtime root: %w", err)
			cancel()
			return
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
				runtimeDir = filepath.Join(root, entry.Name())
				break
			}
		}
		if runtimeDir == "" {
			observerErr = errors.New("ready managed runtime directory is missing")
			cancel()
			return
		}
		for _, name := range []string{"dbus.pid", "sway.pid"} {
			data, readErr := os.ReadFile(filepath.Join(runtimeDir, name))
			if readErr != nil {
				observerErr = fmt.Errorf("read %s: %w", name, readErr)
				cancel()
				return
			}
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil {
				observerErr = fmt.Errorf("parse %s: %w", name, parseErr)
				cancel()
				return
			}
			processIDs = append(processIDs, pid)
		}
		cancel()
	}}

	infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{
		Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond},
	}, false)
	if infra != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("startup after compositor readiness returned infra=%v error=%v, want nil infrastructure and cancellation", infra, err)
	}
	if observerErr != nil {
		t.Fatal(observerErr)
	}
	if !hasStartupEvent(events, startupEvent{StartupPhaseSwaySocketsReady, StartupPhaseSucceeded}) {
		t.Fatalf("startup did not reach compositor readiness: %+v", events)
	}
	if len(processIDs) != 2 {
		t.Fatalf("recorded managed process IDs=%v, want D-Bus and Sway", processIDs)
	}
	for _, pid := range processIDs {
		if pidAlive(pid) {
			t.Errorf("managed process %d survived startup cancellation", pid)
		}
	}
	requireNoManagedSessionDirs(t, root)
}
