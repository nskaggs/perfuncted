package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/v2/internal/runtime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOpenCancellationStopsStaleSessionCleanup(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()

	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	dir, err := os.MkdirTemp(tmp, "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("create stale session directory: %v", err)
	}
	if writeErr := os.WriteFile(filepath.Join(dir, sessionOwnerPIDFile), []byte("99999999"), 0o600); writeErr != nil {
		t.Fatalf("write stale owner pid: %v", writeErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := false
	_, err = openSession(
		ctx,
		WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: time.Second}}),
		WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
			if phase == StartupPhaseStaleSessionCleanup && state == StartupPhaseStarted {
				started = true
				cancel()
			}
		}),
	)
	if !started {
		t.Fatal("Open did not report stale-session cleanup before cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("canceled cleanup removed stale session directory: %v", err)
	}
}

func TestStopRecordedProcessHonorsContextDuringGrace(t *testing.T) {
	dir, err := os.MkdirTemp(t.TempDir(), "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("create managed session directory: %v", err)
	}
	readyPath := filepath.Join(t.TempDir(), "ready")
	signalPath := filepath.Join(t.TempDir(), "term-received")
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = env.Merge(
		os.Environ(),
		"XDG_RUNTIME_DIR="+dir,
		"GO_WANT_HELPER_PROCESS=1",
		"GO_HELPER_READY="+readyPath,
		"GO_HELPER_SIGTERM_MARKER="+signalPath,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start process group: %v", err)
	}
	waitDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waitDone)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
		}
	})
	if err := sessionruntime.WaitForFile(context.Background(), readyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for process helper readiness: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		owned bool
		err   error
	}, 1)
	go func() {
		owned, stopErr := sessionruntime.StopRecordedProcess(ctx, cmd.Process.Pid, dir, time.Hour)
		result <- struct {
			owned bool
			err   error
		}{owned: owned, err: stopErr}
	}()
	if err := sessionruntime.WaitForFile(context.Background(), signalPath, 100, 5*time.Millisecond); err != nil {
		cancel()
		t.Fatalf("wait for TERM delivery: %v", err)
	}
	cancel()
	select {
	case got := <-result:
		if got.owned || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("stop result = (%v, %v), want (false, context.Canceled)", got.owned, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("process-group stop did not return after cancellation")
	}
	if !sessionruntime.ProcessGroupAlive(cmd.Process.Pid) {
		t.Fatal("canceled cleanup escalated to SIGKILL")
	}
}

func TestStopRecordedProcessHonorsCancellationBeforeOwnershipLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	owned, err := sessionruntime.StopRecordedProcess(ctx, 0, "", 0)
	if owned || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled stop = (%v, %v), want (false, context.Canceled)", owned, err)
	}
}

func TestStaleSessionCleanupNilContextAndThrottledCancellation(t *testing.T) {
	err := CleanupStaleSessionsContext(nil, time.Minute) //nolint:staticcheck // Explicit nil-context validation is under test.
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil cleanup context returned %v", err)
	}
	cleanupStaleSessionsMu.Lock()
	savedTime, savedAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = savedTime, savedAttempt
		cleanupStaleSessionsMu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CleanupStaleSessionsContext(ctx, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("throttled canceled cleanup returned %v", err)
	}
}

// An unreadable runtime root is a scan failure, not a successful cleanup.
// The claimed throttle window must be released so a repaired environment can
// be scanned again immediately rather than suppressing stale-session recovery.
func TestStaleSessionScanFailureAllowsImmediateCorrectedRootRetry(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	priorTime, priorAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = priorTime, priorAttempt
		cleanupStaleSessionsMu.Unlock()
	})

	parent := t.TempDir()
	badRoot := filepath.Join(parent, "not-a-directory")
	if err := os.WriteFile(badRoot, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", badRoot)
	if err := CleanupStaleSessionsContext(context.Background(), 0); err == nil {
		t.Fatal("invalid runtime root was silently accepted")
	}
	cleanupStaleSessionsMu.Lock()
	released := lastCleanupTime.IsZero()
	cleanupStaleSessionsMu.Unlock()
	if !released {
		t.Fatal("failed scan retained cleanup throttle claim")
	}

	goodRoot := filepath.Join(parent, "valid-root")
	if err := os.Mkdir(goodRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", goodRoot)
	dir := filepath.Join(goodRoot, "perfuncted-xdg-stale")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	if err := CleanupStaleSessionsContext(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("corrected root was not scanned after failure: %v", err)
	}
	data, err := os.ReadFile(badRoot)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("unrelated runtime file changed: %q %v", data, err)
	}
}

func TestStaleCleanupPreservesRuntimeUsedByUnownedProcessGroup(t *testing.T) {
	runtimeRoot := t.TempDir()
	t.Setenv("TMPDIR", runtimeRoot)
	dir, err := os.MkdirTemp(runtimeRoot, "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("create managed runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionOwnerPIDFile), []byte("2147483647"), 0o600); err != nil {
		t.Fatalf("write stale owner PID: %v", err)
	}

	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = env.Merge(
		os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		"GO_HELPER_READY="+readyPath,
		"XDG_RUNTIME_DIR="+dir,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start runtime user: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if err := sessionruntime.WaitForFile(context.Background(), readyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for runtime user readiness: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sway.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatalf("write child PID: %v", err)
	}

	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	if err := CleanupStaleSessionsContext(context.Background(), 24*time.Hour); err != nil {
		t.Fatalf("cleanup stale sessions: %v", err)
	}

	if err := syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatalf("cleanup terminated a process it does not own: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cleanup removed runtime still referenced by a live child: %v", err)
	}
}

type cancelAtErrCallContext struct {
	context.Context
	cancel context.CancelCauseFunc
	target int
	calls  int
}

func (c *cancelAtErrCallContext) Err() error {
	c.calls++
	if c.calls == c.target {
		c.cancel(errors.New("caller withdrew cleanup authority"))
	}
	return c.Context.Err()
}

func TestRateLimitedSessionCleanupPreservesConcurrentCancellation(t *testing.T) {
	cleanupStaleSessionsMu.Lock()
	previousTime, previousAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Now()
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = previousTime, previousAttempt
		cleanupStaleSessionsMu.Unlock()
	})

	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
	err := CleanupStaleSessionsContext(ctx, time.Minute)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) {
		t.Fatalf("rate-limited cleanup error = %v, want caller cancellation and cause", err)
	}
}

// The scanner may finish successfully at the exact instant the caller revokes
// authorization. The final check must reject that scan and release its retry
// throttle, rather than reporting cleanup success.
type cancelOnSecondTopLevelCleanupCheck struct {
	context.Context
	cancel context.CancelCauseFunc
	calls  int
}

func (c *cancelOnSecondTopLevelCleanupCheck) Err() error {
	pc, _, _, ok := runtime.Caller(1)
	if ok {
		fn := runtime.FuncForPC(pc)
		if fn != nil && strings.HasSuffix(fn.Name(), ".CleanupStaleSessionsContext") {
			c.calls++
			if c.calls == 2 {
				c.cancel(errors.New("revoked after scan"))
			}
		}
	}
	return c.Context.Err()
}

func TestStaleSessionCleanupRejectsCancellationAfterSuccessfulScan(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cleanupStaleSessionsMu.Lock()
	priorTime, priorAttempt := lastCleanupTime, cleanupAttempt
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime, cleanupAttempt = priorTime, priorAttempt
		cleanupStaleSessionsMu.Unlock()
	})
	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelOnSecondTopLevelCleanupCheck{Context: base, cancel: cancel}
	err := CleanupStaleSessionsContext(ctx, time.Hour)
	if !errors.Is(err, context.Canceled) || ctx.calls != 2 {
		t.Fatalf("cleanup completed after final revocation: err=%v top-level checks=%d", err, ctx.calls)
	}
	cleanupStaleSessionsMu.Lock()
	released := lastCleanupTime.IsZero()
	cleanupStaleSessionsMu.Unlock()
	if !released {
		t.Fatal("failed cleanup retained stale-scan throttle")
	}
}

// Retention scans a caller-supplied parent. Expired private session folders
// may be removed, but unrelated files, symlinks, and young sessions must not
// be removed even when their names resemble managed session directories.
func TestSessionLogRetentionPreservesForeignEntriesAndRecentSessions(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	old := filepath.Join(root, sessionruntime.SessionLogPrefix+"expired")
	recent := filepath.Join(root, sessionruntime.SessionLogPrefix+"recent")
	foreign := filepath.Join(root, "caller-owned")
	externalData := filepath.Join(external, "important")
	for _, directory := range []string{old, recent, foreign} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(externalData, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, sessionruntime.SessionLogPrefix+"external-link")
	if err := os.Symlink(external, alias); err != nil {
		t.Fatal(err)
	}
	matchingFile := filepath.Join(root, sessionruntime.SessionLogPrefix+"ordinary-file")
	if err := os.WriteFile(matchingFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-72 * time.Hour)
	for _, path := range []string{old, foreign, matchingFile} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	CleanupSessionLogs(root, 24*time.Hour)
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired managed session remains: %v", err)
	}
	for _, path := range []string{recent, foreign, matchingFile, alias, externalData} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("retention touched unrelated or recent entry %q: %v", path, err)
		}
	}
	if bytes, err := os.ReadFile(externalData); err != nil || string(bytes) != "untouched" {
		t.Fatalf("symlink target modified: %q %v", bytes, err)
	}
}

func TestSessionLogRetentionDisabledOrMissingRootNeverRemovesEntries(t *testing.T) {
	root := t.TempDir()
	expired := filepath.Join(root, sessionruntime.SessionLogPrefix+"old")
	if err := os.Mkdir(expired, 0700); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(expired, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		root string
		age  time.Duration
	}{{root, 0}, {root, -time.Second}, {"", time.Second}, {filepath.Join(root, "nonexistent"), time.Second}} {
		CleanupSessionLogs(tc.root, tc.age)
		if _, err := os.Stat(expired); err != nil {
			t.Fatalf("disabled/missing root removed existing directory: %v", err)
		}
	}
}

func TestSessionLogRootRejectsSymlinksAndRegularFiles(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "regular")
	if err := os.WriteFile(file, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{alias, file} {
		if dir, err := sessionruntime.CreateSessionLogDir(path); err == nil {
			_ = os.RemoveAll(dir)
			t.Fatalf("unsafe log root accepted: %q", path)
		}
	}
	if entries, err := os.ReadDir(real); err != nil || len(entries) != 0 {
		t.Fatalf("symlink root altered target: %v %v", entries, err)
	}
}
