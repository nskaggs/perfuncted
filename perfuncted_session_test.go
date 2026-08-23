package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestEnviron(t *testing.T) { //nolint:gocyclo
	t.Parallel()
	ev := env.Environ("/tmp/test-xdg", "wayland-99", "unix:path=/tmp/test-xdg/bus")

	var xdg, wl, dbus, display, gdk, qt string
	var sessionType string
	for _, e := range ev {
		switch {
		case strings.HasPrefix(e, "XDG_RUNTIME_DIR="):
			xdg = strings.TrimPrefix(e, "XDG_RUNTIME_DIR=")
		case strings.HasPrefix(e, "WAYLAND_DISPLAY="):
			wl = strings.TrimPrefix(e, "WAYLAND_DISPLAY=")
		case strings.HasPrefix(e, "DBUS_SESSION_BUS_ADDRESS="):
			dbus = strings.TrimPrefix(e, "DBUS_SESSION_BUS_ADDRESS=")
		case strings.HasPrefix(e, "DISPLAY="):
			display = strings.TrimPrefix(e, "DISPLAY=")
		case strings.HasPrefix(e, "GDK_BACKEND="):
			gdk = strings.TrimPrefix(e, "GDK_BACKEND=")
		case strings.HasPrefix(e, "QT_QPA_PLATFORM="):
			qt = strings.TrimPrefix(e, "QT_QPA_PLATFORM=")
		case strings.HasPrefix(e, "XDG_SESSION_TYPE="):
			sessionType = strings.TrimPrefix(e, "XDG_SESSION_TYPE=")
		}
	}

	if xdg != "/tmp/test-xdg" {
		t.Errorf("XDG_RUNTIME_DIR = %q, want /tmp/test-xdg", xdg)
	}
	if wl != "wayland-99" {
		t.Errorf("WAYLAND_DISPLAY = %q, want wayland-99", wl)
	}
	if dbus != "unix:path=/tmp/test-xdg/bus" {
		t.Errorf("DBUS_SESSION_BUS_ADDRESS = %q", dbus)
	}
	if display != "" {
		t.Errorf("DISPLAY = %q, want empty (cleared)", display)
	}
	if gdk != "wayland" {
		t.Errorf("GDK_BACKEND = %q, want wayland", gdk)
	}
	if qt != "wayland" {
		t.Errorf("QT_QPA_PLATFORM = %q, want wayland", qt)
	}
	if sessionType != "wayland" {
		t.Errorf("XDG_SESSION_TYPE = %q, want wayland", sessionType)
	}
}

func TestEnvironFiltersHost(t *testing.T) {
	// Set some vars that should be filtered.
	os.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	os.Setenv("WAYLAND_DISPLAY", "wayland-0")
	os.Setenv("DISPLAY", ":0")
	defer os.Unsetenv("DISPLAY")

	ev := env.Environ("/tmp/sess", "wayland-1", "unix:path=/tmp/sess/bus")

	// Count occurrences of XDG_RUNTIME_DIR — should be exactly 1.
	count := 0
	for _, e := range ev {
		if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("XDG_RUNTIME_DIR appears %d times, want 1", count)
	}
}

func TestSessionConfigDefaults(t *testing.T) {
	cfg := SessionConfig{}
	if cfg.Resolution != (image.Point{}) {
		t.Errorf("default Resolution = %v, want zero", cfg.Resolution)
	}
	if cfg.LogDir != "" {
		t.Errorf("default LogDir = %q, want empty", cfg.LogDir)
	}
}

func TestEmbeddedConfigs(t *testing.T) {
	data, err := embeddedConfigs.ReadFile("configs/headless.conf")
	if err != nil {
		t.Fatalf("read embedded headless.conf: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded headless.conf is empty")
	}
	if !strings.Contains(string(data), "HEADLESS-1") {
		t.Error("headless.conf missing HEADLESS-1 output line")
	}

	data, err = embeddedConfigs.ReadFile("configs/nested.conf")
	if err != nil {
		t.Fatalf("read embedded nested.conf: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded nested.conf is empty")
	}
}

func TestStopManagedProcessReapsChild(t *testing.T) {
	infra := &sessionInfra{}
	cmd := helperCommand(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	infra.stopManagedProcess(newManagedSessionProcess(cmd), 500*time.Millisecond)
	if err := syscall.Kill(cmd.Process.Pid, 0); err != syscall.ESRCH {
		t.Fatalf("expected process to be gone, got %v", err)
	}
}

func TestManagedSessionProcessReapsNaturallyExitingChild(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	proc := newManagedSessionProcess(cmd)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := proc.wait(ctx); err != nil {
		t.Fatalf("wait for helper: %v", err)
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatal("managed session process did not record the child exit")
	}

	var status syscall.WaitStatus
	if _, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WNOHANG, nil); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("Wait4 after managed reap error = %v, want ECHILD", err)
	}
}

func TestManagedProcStopReapsProcessGroupChildren(t *testing.T) {
	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(
		"sh",
		"-c",
		`(trap '' TERM; sleep 30) & child=$!; printf '%s' "$child" > "$CHILD_PID"; exit 0`,
	)
	cmd.Env = env.Merge(os.Environ(), "CHILD_PID="+childPIDPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start process group: %v", err)
	}
	childPID := 0
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})
	childPID, err := waitForPIDFile(
		context.Background(),
		childPIDPath,
	)
	if err != nil {
		t.Fatalf("wait for child pid: %v", err)
	}

	(&managedProc{cmd: cmd, pid: cmd.Process.Pid}).stop(100 * time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for pidAlive(childPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pidAlive(childPID) {
		t.Fatalf("process-group child %d survived managed shutdown", childPID)
	}
}

func TestApplicationStopWaitsForGroupAfterLeaderExit(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	childReadyPath := filepath.Join(t.TempDir(), "child.ready")
	app, err := session.Launch(
		context.Background(),
		Command{
			Name: os.Args[0],
			Args: []string{"-test.run=TestHelperProcess", "--"},
			Env: []string{
				"GO_WANT_HELPER_PROCESS=1",
				"GO_HELPER_CHILD_PID=" + childPIDPath,
				"GO_HELPER_CHILD_READY=" + childReadyPath,
			},
		},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	childPID, err := waitForPIDFile(
		context.Background(),
		childPIDPath,
	)
	if err != nil {
		t.Fatalf("wait for child pid: %v", err)
	}
	if err := waitForFile(context.Background(), childReadyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for child readiness: %v", err)
	}
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("Wait for leader: %v", err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	stopErr := app.Stop(stopCtx)
	if !errors.Is(stopErr, context.DeadlineExceeded) {
		t.Fatalf("Stop error = %v, want context deadline after descendant ignored SIGTERM", stopErr)
	}
	if err := app.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := app.proc.waitGroup(waitCtx); err != nil {
		t.Fatalf("wait for killed process group: %v", err)
	}
	if pidAlive(childPID) {
		t.Fatalf("process-group child %d survived Kill", childPID)
	}
}

func TestSessionCloseEscalatesApplicationGroupAfterLeaderExit(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	session.config.ApplicationGracePeriod = 200 * time.Millisecond

	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	childReadyPath := filepath.Join(t.TempDir(), "child.ready")
	app, err := session.Launch(
		context.Background(),
		Command{
			Name: os.Args[0],
			Args: []string{"-test.run=TestHelperProcess", "--"},
			Env: []string{
				"GO_WANT_HELPER_PROCESS=1",
				"GO_HELPER_CHILD_PID=" + childPIDPath,
				"GO_HELPER_CHILD_READY=" + childReadyPath,
			},
		},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	childPID, err := waitForPIDFile(
		context.Background(),
		childPIDPath,
	)
	if err != nil {
		t.Fatalf("wait for child pid: %v", err)
	}
	if err := waitForFile(context.Background(), childReadyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for child readiness: %v", err)
	}
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("Wait for leader: %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if app.proc.groupAlive() {
		t.Fatal("application process group survived Session.Close")
	}
	if pidAlive(childPID) {
		t.Fatalf("process-group child %d survived Session.Close", childPID)
	}
}

func waitForPIDFile(
	ctx context.Context,
	path string,
) (int, error) {
	const attempts = 100
	const interval = 5 * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		pid, err := readPIDFile(path)
		if err == nil {
			return pid, nil
		}
		lastErr = err
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
		}
	}
	return 0, fmt.Errorf("read pid file %s: %w", path, lastErr)
}

func helperCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = env.Merge(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if childPIDPath := os.Getenv("GO_HELPER_CHILD_PID"); childPIDPath != "" {
		child := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
		child.Env = env.Merge(
			os.Environ(),
			"GO_HELPER_CHILD_PID=",
			"GO_WANT_HELPER_PROCESS=1",
			"GO_WANT_IGNORE_SIGTERM=1",
		)
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(
			childPIDPath,
			[]byte(strconv.Itoa(child.Process.Pid)),
			0o600,
		); err != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			os.Exit(2)
		}
		if os.Getenv("GO_HELPER_HOLD_UNTIL_TERM") != "1" {
			os.Exit(0)
		}
		_ = os.Unsetenv("GO_HELPER_CHILD_READY")
	}
	if readyPath := os.Getenv("GO_HELPER_READY"); readyPath != "" {
		if err := os.WriteFile(readyPath, nil, 0o600); err != nil {
			os.Exit(2)
		}
	}
	if os.Getenv("GO_WANT_IGNORE_SIGTERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	if readyPath := os.Getenv("GO_HELPER_CHILD_READY"); readyPath != "" {
		if err := os.WriteFile(readyPath, nil, 0o600); err != nil {
			os.Exit(2)
		}
	}
	for {
		time.Sleep(10 * time.Second)
	}
}

func TestCleanupOnSignalStopsOnContextCancel(t *testing.T) {
	xdgDir := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdgDir, 0700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}
	infra := &sessionInfra{xdgDir: xdgDir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	unregister := infra.CleanupOnSignal(ctx)
	defer unregister()
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		infra.mu.Lock()
		stopped := infra.stopped
		infra.mu.Unlock()
		if stopped {
			if _, err := os.Stat(xdgDir); os.IsNotExist(err) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session was not stopped on context cancellation")
}

func TestSessionStopUnregistersAutoSignalHandler(t *testing.T) {
	xdgDir := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdgDir, 0o700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}
	infra := &sessionInfra{xdgDir: xdgDir}
	infra.unregister = infra.CleanupOnSignal(context.Background())

	infra.stop()

	if infra.unregister != nil {
		t.Fatal("signal handler unregister function was not cleared")
	}
}

func TestSessionCleanupRemovesXDGRuntimeDir(t *testing.T) {
	xdgDir := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdgDir, 0o700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}

	infra := &sessionInfra{xdgDir: xdgDir}
	infra.stop()

	infra.mu.Lock()
	stopped := infra.stopped
	infra.mu.Unlock()
	if !stopped {
		t.Fatal("session was not marked stopped")
	}
	if _, err := os.Stat(xdgDir); !os.IsNotExist(err) {
		t.Fatalf("xdg dir still exists after cleanup: %v", err)
	}
}

func TestSessionStopNil(t *testing.T) {
	var infra *sessionInfra
	infra.stop()
}

func TestProcessUsesRuntimeDirAt(t *testing.T) {
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "123")
	if err := os.Mkdir(pidDir, 0o700); err != nil {
		t.Fatalf("mkdir process dir: %v", err)
	}
	environPath := filepath.Join(pidDir, "environ")

	writeEnvironment := func(values ...string) {
		t.Helper()
		if err := os.WriteFile(environPath, []byte(strings.Join(values, "\x00")+"\x00"), 0o600); err != nil {
			t.Fatalf("write environ: %v", err)
		}
	}

	writeEnvironment("PATH=/usr/bin", "XDG_RUNTIME_DIR=/tmp/perfuncted-xdg-owned")
	if !processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("matching runtime directory was not recognized")
	}
	if processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-other") {
		t.Fatal("mismatched runtime directory was accepted")
	}

	writeEnvironment("PATH=/usr/bin", "OTHER=/tmp/perfuncted-xdg-owned")
	if processUsesRuntimeDirAt(procRoot, 123, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("runtime directory value under another key was accepted")
	}
	if processUsesRuntimeDirAt(procRoot, 999, "/tmp/perfuncted-xdg-owned") {
		t.Fatal("missing process was accepted")
	}
}

func TestStartNestedSessionCompiles(t *testing.T) {
	t.Skip("requires display server (Wayland)")
	_, _ = Open(context.Background(), WithNested(SessionConfig{}))
}

func TestCleanupStaleSessionsRemovesDeadPIDDir(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	if err := os.WriteFile(filepath.Join(dir, "perfuncted.pid"), []byte("99999999"), 0o600); err != nil {
		t.Fatalf("WriteFile pid: %v", err)
	}

	CleanupStaleSessions(24 * time.Hour)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale session dir still exists: %v", err)
	}
}

func TestCleanupStaleSessionsConcurrentNoPidfileIsIdempotent(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			CleanupStaleSessions(24 * time.Hour)
		}()
	}
	wg.Wait()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale no-pidfile session dir still exists: %v", err)
	}
}

func TestCleanupStaleSessionsKeepsRecentNoPidfileDir(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	recent := time.Now().Add(-1 * time.Minute)
	if err := os.Chtimes(dir, recent, recent); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	CleanupStaleSessions(24 * time.Hour)

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("recent no-pidfile session dir was removed: %v", err)
	}
}

func TestCleanupStaleSessionsClampsUnsafeMetadataAge(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, sessionOwnerPIDFile), []byte("invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile owner pid: %v", err)
	}
	recent := time.Now().Add(-time.Minute)
	if err := os.Chtimes(dir, recent, recent); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	CleanupStaleSessions(0)

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("recent malformed-owner session dir was removed: %v", err)
	}
}

func TestCleanupStaleSessionsReapsNoPidfileAfterGrace(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	stale := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(dir, stale, stale); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	CleanupStaleSessions(24 * time.Hour)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale no-pidfile session dir still exists: %v", err)
	}
}

func TestCleanupStaleSessionsReapsRecordedGroupAfterLeaderExit(t *testing.T) { //nolint:gocyclo
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if writeErr := os.WriteFile(
		filepath.Join(dir, sessionOwnerPIDFile),
		[]byte("99999999"),
		0o600,
	); writeErr != nil {
		t.Fatalf("WriteFile owner pid: %v", writeErr)
	}

	childPIDPath := filepath.Join(t.TempDir(), "child.pid")
	childReadyPath := filepath.Join(t.TempDir(), "child.ready")
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = env.Merge(
		os.Environ(),
		"XDG_RUNTIME_DIR="+dir,
		"GO_WANT_HELPER_PROCESS=1",
		"GO_HELPER_CHILD_PID="+childPIDPath,
		"GO_HELPER_CHILD_READY="+childReadyPath,
		"GO_HELPER_HOLD_UNTIL_TERM=1",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if startErr := cmd.Start(); startErr != nil {
		t.Fatalf("start process group: %v", startErr)
	}
	childPID, err := waitForPIDFile(
		context.Background(),
		childPIDPath,
	)
	if err != nil {
		t.Fatalf("wait for child pid: %v", err)
	}
	if err := waitForFile(context.Background(), childReadyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for child readiness: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "sway.pid"),
		[]byte(strconv.Itoa(cmd.Process.Pid)),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile child pid: %v", err)
	}
	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitCh)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
	})

	CleanupStaleSessions(24 * time.Hour)

	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("recorded process group leader survived cleanup")
	}
	deadline := time.Now().Add(2 * time.Second)
	for pidAlive(childPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pidAlive(childPID) {
		t.Fatalf("recorded process-group child %d survived cleanup", childPID)
	}
	if processGroupAlive(cmd.Process.Pid) {
		t.Fatal("recorded process group survived cleanup")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale session dir still exists: %v", err)
	}
}

func TestCleanupStaleSessionsTerminatesRecordedChildren(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	if err := os.WriteFile(filepath.Join(dir, sessionOwnerPIDFile), []byte("99999999"), 0o600); err != nil {
		t.Fatalf("WriteFile owner pid: %v", err)
	}

	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "XDG_RUNTIME_DIR="+dir, "GO_HELPER_READY="+readyPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitCh)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
	})
	if err := waitForFile(context.Background(), readyPath, 100, 10*time.Millisecond); err != nil {
		t.Fatalf("wait for helper readiness: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sway.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatalf("WriteFile child pid: %v", err)
	}

	CleanupStaleSessions(24 * time.Hour)

	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("recorded child process still alive")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale session dir still exists: %v", err)
	}
}

func TestCleanupStaleSessionsEscalatesForOwnedChild(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, sessionOwnerPIDFile), []byte("99999999"), 0o600); err != nil {
		t.Fatalf("WriteFile owner pid: %v", err)
	}

	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(
		cmd.Env,
		"XDG_RUNTIME_DIR="+dir,
		"GO_WANT_IGNORE_SIGTERM=1",
		"GO_HELPER_READY="+readyPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	waitCh := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitCh)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
	})
	if err := waitForFile(context.Background(), readyPath, 100, 10*time.Millisecond); err != nil {
		t.Fatalf("wait for helper readiness: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, "sway.pid"),
		[]byte(strconv.Itoa(cmd.Process.Pid)),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile child pid: %v", err)
	}

	CleanupStaleSessions(24 * time.Hour)

	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		t.Fatal("owned child process ignored cleanup escalation")
	}
}

func TestCleanupStaleSessionsDoesNotTerminateMismatchedChild(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(
		filepath.Join(dir, sessionOwnerPIDFile),
		[]byte("99999999"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile owner pid: %v", err)
	}

	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "XDG_RUNTIME_DIR=/tmp/perfuncted-xdg-other")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	if err := os.WriteFile(
		filepath.Join(dir, "sway.pid"),
		[]byte(strconv.Itoa(cmd.Process.Pid)),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile child pid: %v", err)
	}

	CleanupStaleSessions(24 * time.Hour)

	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("mismatched child process was terminated: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("stale session dir still exists: %v", err)
	}
}

func TestCleanupStaleSessionsKeepsLivePIDDir(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	if err := os.WriteFile(filepath.Join(dir, "perfuncted.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile pid: %v", err)
	}

	CleanupStaleSessions(0)

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("live session dir was removed: %v", err)
	}
}
