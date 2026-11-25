package perfuncted

import (
	"context"
	"errors"
	"fmt"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
	"image"
	"net"
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
		if sessionruntime.PIDAlive(pid) {
			t.Errorf("managed process %d survived startup cancellation", pid)
		}
	}
	requireNoManagedSessionDirs(t, root)
}

// A startup error and its cleanup error are both authoritative. The caller
// must not be told only about the launch failure while a managed runtime
// remains on disk and may still have resources attached.
func TestStartSessionFailureReportsCleanupError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires ordinary filesystem permissions")
	}
	root, err := os.MkdirTemp("/var/tmp", "pft-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	t.Setenv("TMPDIR", root)

	var managed string
	var prepErr error
	s := NewSessionForTesting(nil, nil, nil, nil, nil)
	s.startupObserver = func(phase StartupPhase, state StartupPhaseState) {
		if phase != StartupPhaseRuntimeDirectoryPreparation || state != StartupPhaseSucceeded {
			return
		}
		entries, listErr := os.ReadDir(root)
		if listErr != nil {
			prepErr = listErr
			return
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
				continue
			}
			managed = filepath.Join(root, entry.Name())
			// The sentinel makes removal require write permission *inside*
			// the runtime directory, unlike removing an empty directory.
			if prepErr = os.WriteFile(filepath.Join(managed, "sentinel"), []byte("retain"), 0600); prepErr != nil {
				return
			}
			prepErr = os.Chmod(managed, 0500)
			return
		}
	}
	defer func() {
		if managed != "" {
			_ = os.Chmod(managed, 0700)
			_ = os.RemoveAll(managed)
		}
	}()
	infra, err := s.startSession(context.Background(), sessionModeHeadless,
		SessionConfig{LogDir: filepath.Join(root, "logs")}, false)
	if prepErr != nil {
		t.Fatalf("fixture setup: %v", prepErr)
	}
	if managed == "" {
		t.Fatal("startup did not produce an isolated managed runtime")
	}
	if infra != nil {
		t.Fatal("failed startup returned a live session")
	}
	if err == nil || !strings.Contains(err.Error(), "write owner pidfile") {
		t.Fatalf("startSession error=%v, expected forced write failure", err)
	}
	if !strings.Contains(err.Error(), "remove managed runtime") {
		t.Fatalf("startSession lost its cleanup failure: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(managed, "sentinel")); statErr != nil {
		t.Fatalf("fixture no longer exhibits cleanup failure: %v", statErr)
	}
}

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

// An explicitly supplied Sway config must not be replaced by an embedded
// compositor config or copied into the managed runtime. Cancellation must
// still prevent configuration selection, including explicit overrides.
func TestManagedSessionExplicitSwayConfigPreservesCallerFileAndCancellation(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	callerConfig := filepath.Join(t.TempDir(), "user-sway.conf")
	const original = "# user selected config\noutput * resolution 1280x720\n"
	if err := os.WriteFile(callerConfig, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	session := &Session{}
	infra := &sessionInfra{xdgDir: runtimeDir}
	config := SessionConfig{SwayConfigPath: callerConfig, Resolution: image.Pt(1920, 1080)}
	for _, mode := range []sessionMode{sessionModeHeadless, sessionModeNested} {
		got, err := session.resolveSessionSwayConfig(context.Background(), infra, mode, config)
		if err != nil || got != callerConfig {
			t.Fatalf("explicit config mode %d: path=%q error=%v", mode, got, err)
		}
	}
	if data, err := os.ReadFile(callerConfig); err != nil || string(data) != original {
		t.Fatalf("caller configuration mutated: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "sway.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unrequested generated config exists: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := session.resolveSessionSwayConfig(ctx, infra, sessionModeHeadless, config); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled config selected: %q %v", got, err)
	}
}

// Managed runtime paths are literal. Glob metacharacters in their ancestors
// must not redirect socket discovery to another session or hide a real socket.
func TestSwaySocketUsesLiteralRuntimeDirectory(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pf-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	own := filepath.Join(root, "private[ab]")
	foreign := filepath.Join(root, "privatea")
	for _, dir := range []string{own, foreign} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	listen := func(dir string) (string, net.Listener) {
		path := filepath.Join(dir, "sway-ipc.123.sock")
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		return path, listener
	}
	outside, foreignListener := listen(foreign)
	defer foreignListener.Close()
	expected, ownListener := listen(own)
	defer ownListener.Close()
	session := &Session{infra: &sessionInfra{xdgDir: own}}
	if got := session.SwaySocket(); got != expected {
		t.Fatalf("runtime %q resolved socket %q, want own %q (foreign %q)", own, got, expected, outside)
	}
}

func TestSwaySocketSupportsInvalidGlobCharacterInLiteralRuntime(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfsock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	own := filepath.Join(root, "private[unfinished")
	if err := os.Mkdir(own, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(own, "sway-ipc.local.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	session := &Session{infra: &sessionInfra{xdgDir: own}}
	if got := session.SwaySocket(); got != path {
		t.Fatalf("literal bracket runtime socket=%q want %q", got, path)
	}
}
