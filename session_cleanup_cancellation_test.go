package perfuncted

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
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
	_, err = Open(
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
	if err := waitForFile(context.Background(), readyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for process helper readiness: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		owned bool
		err   error
	}, 1)
	go func() {
		owned, stopErr := stopRecordedProcess(ctx, cmd.Process.Pid, dir, time.Hour)
		result <- struct {
			owned bool
			err   error
		}{owned: owned, err: stopErr}
	}()
	if err := waitForFile(context.Background(), signalPath, 100, 5*time.Millisecond); err != nil {
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
	if !processGroupAlive(cmd.Process.Pid) {
		t.Fatal("canceled cleanup escalated to SIGKILL")
	}
}
