package perfuncted

import (
	"context"
	"errors"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A session's runtime can already have been removed by independent stale
// cleanup. Stop must join successfully instead of interpreting the missing
// path as an unsafe replacement or trying to remove neighboring files.
func TestManagedSessionStopToleratesAlreadyRemovedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", shortPrivateRuntimeDir(t))
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(root, "unrelated")
	if err := os.WriteFile(neighbor, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(runtimeDir); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	if err := infra.stop(); err != nil {
		t.Fatalf("already removed runtime stop: %v", err)
	}
	if err := infra.stop(); err != nil {
		t.Fatalf("idempotent stop: %v", err)
	}
	data, err := os.ReadFile(neighbor)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("neighbor modified during absent-runtime teardown: %q err=%v", data, err)
	}
	if _, err := os.Lstat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime resurrected by stop: %v", err)
	}
}

// There is no handler to register for an uninitialized infrastructure. The
// unregister callback must still be safe to invoke repeatedly.
func TestNilManagedInfraSignalCleanupReturnsHarmlessUnregister(t *testing.T) {
	var infra *sessionInfra
	unregister := infra.CleanupOnSignal(context.Background())
	if unregister == nil {
		t.Fatal("nil infrastructure returned no unregister callback")
	}
	unregister()
	unregister()
}

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

func TestManagedSwayLogCreationFailureStopsBeforeProcessSpawn(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	logBlocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(logBlocker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{
		xdgDir:    runtimeDir,
		wlDisplay: "wayland-1",
		logDir:    logBlocker,
		ctx:       context.Background(),
	}
	err = infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionModeHeadless)
	if err == nil || !strings.Contains(err.Error(), "create log") {
		t.Fatalf("startup with an unusable log directory returned %v", err)
	}
	if infra.swayCmd != nil {
		t.Fatal("Sway was started before its private log could be created")
	}
	if contents, readErr := os.ReadFile(logBlocker); readErr != nil || string(contents) != "preserve" {
		t.Fatalf("log setup failure changed its blocking file: %q %v", contents, readErr)
	}
}

func TestManagedSwayConfigPublishErrorPreservesObstructingDirectory(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	target := filepath.Join(runtimeDir, "sway.conf")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "preserve")
	if err := os.WriteFile(sentinel, []byte("existing state"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath, err := (&sessionInfra{xdgDir: runtimeDir}).writeEmbeddedConfig("configs/headless.conf", image.Point{})
	if err == nil || configPath != "" || !strings.Contains(err.Error(), "write config") {
		t.Fatalf("config publication over a directory returned path=%q err=%v", configPath, err)
	}
	if contents, readErr := os.ReadFile(sentinel); readErr != nil || string(contents) != "existing state" {
		t.Fatalf("failed config publication changed obstructing state: %q %v", contents, readErr)
	}
	entries, readErr := os.ReadDir(runtimeDir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "sway.conf" || !entries[0].IsDir() {
		t.Fatalf("failed config publication leaked staged data or replaced the directory: %v %v", entries, readErr)
	}
}

// An invalid caller-selected temporary root must stop managed startup before
// any log allocation. The setup wrapper must preserve the underlying error
// and leave both output paths uninitialized, not fall back to an unrelated root.
func TestManagedRuntimePreparationRejectsMissingPreferredTempRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-preferred-temporary-root")
	t.Setenv("TMPDIR", missing)
	logs := filepath.Join(t.TempDir(), "uncreated-private-logs")
	var runtimeDir, logDir string
	err := prepareSessionRuntimeDirs(&runtimeDir, &logDir, logs)
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "managed runtime") {
		t.Fatalf("missing caller temp root: %v", err)
	}
	if runtimeDir != "" || logDir != "" {
		t.Fatalf("failed startup published runtime=%q logs=%q", runtimeDir, logDir)
	}
	if _, statErr := os.Lstat(logs); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed startup allocated log root: %v", statErr)
	}
}

// A matching managed-runtime name is insufficient authority to create PID
// metadata or compositor config in a directory another account can modify.
func TestManagedSessionWritersRejectOtherWritableRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name  string
		mode  os.FileMode
		allow bool
	}{
		{"private", 0700, true},
		{"group-writable", 0770, false},
		{"world-writable", 0777, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "perfuncted-xdg-"+tc.name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			infra := &sessionInfra{xdgDir: dir}
			infra.writeChildPID("dbus.pid", os.Getpid())
			_, pidErr := os.Lstat(filepath.Join(dir, "dbus.pid"))
			_, configErr := infra.writeEmbeddedConfig("configs/headless.conf", image.Pt(1280, 720))
			_, confStatErr := os.Lstat(filepath.Join(dir, "sway.conf"))
			if tc.allow {
				if pidErr != nil || configErr != nil || confStatErr != nil {
					t.Fatalf("private runtime rejected: pid=%v config=%v stat=%v", pidErr, configErr, confStatErr)
				}
			} else {
				if !os.IsNotExist(pidErr) || configErr == nil || !os.IsNotExist(confStatErr) {
					t.Fatalf("writable runtime admitted: pid=%v config=%v stat=%v", pidErr, configErr, confStatErr)
				}
			}
		})
	}
}

// Invalid caller log storage must not leak the freshly allocated managed
// desktop runtime, and an already canceled operation must allocate nothing.
func TestManagedSessionPreparationAndCancellationFailBeforeSpawning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session := &Session{}
	if infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{}, false); infra != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup: infra=%v err=%v", infra, err)
	}
	root := t.TempDir()
	blocker := filepath.Join(root, "foreign-file")
	if err := os.WriteFile(blocker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	var runtimeDir, logDir string
	err := prepareSessionRuntimeDirs(&runtimeDir, &logDir, filepath.Join(blocker, "logs"))
	if err == nil || runtimeDir == "" || logDir != "" {
		t.Fatalf("unsafe log root accepted: runtime=%q logs=%q err=%v", runtimeDir, logDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	if data, readErr := os.ReadFile(blocker); readErr != nil || string(data) != "preserve" {
		t.Fatalf("foreign file altered: %q %v", data, readErr)
	}
	if err := os.RemoveAll(runtimeDir); err != nil {
		t.Fatal(err)
	}
}

func TestManagedSessionInvalidConfigurationAndDaemonAddressNeverSpawn(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	infra := &sessionInfra{xdgDir: runtimeDir, dbusAddr: "tcp:host=example.org", ctx: context.Background()}
	if err := infra.launchDBus(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid D-Bus socket address") || infra.dbusCmd != nil {
		t.Fatalf("invalid daemon address admitted: %v", err)
	}
	if _, err := infra.writeEmbeddedConfig("configs/not-a-configuration.conf", image.Point{}); err == nil || !strings.Contains(err.Error(), "read embedded config") {
		t.Fatalf("missing embedded config accepted: %v", err)
	}
	if _, err := (&Session{}).resolveSessionSwayConfig(context.Background(), infra, sessionMode(-8), SessionConfig{}); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("invalid compositor mode accepted: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(runtimeDir, "sway.conf")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid config was written: %v", statErr)
	}
}

func TestManagedRuntimeCreationRefusesUnusableTemporaryRoot(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing-root"))
	if dir, err := createManagedSessionRuntimeDir(); err == nil || dir != "" {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		t.Fatalf("missing caller temp root accepted: dir=%q err=%v", dir, err)
	}
	t.Setenv("TMPDIR", "relative-root")
	roots := sessionruntime.ManagedSessionTempRoots()
	if len(roots) != 1 || roots[0] != "/tmp" {
		t.Fatalf("relative socket root accepted: %+v", roots)
	}
}

func TestManagedChildPIDWriteFailurePreservesExistingDirectory(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	target := filepath.Join(runtimeDir, "child.pid")
	if mkdirErr := os.Mkdir(target, 0700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	(&sessionInfra{xdgDir: runtimeDir}).writeChildPID("child.pid", os.Getpid())
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("pid writer replaced foreign directory: %+v %v", info, err)
	}
}
