package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTraceAndNestedOptionsKeepConfigurationAndRejectTargetCollision(t *testing.T) {
	cfg := &openConfig{}
	target := SessionConfig{}
	for _, option := range []Option{WithNested(target), WithTrace(true), WithTraceDelay(17 * time.Millisecond)} {
		if err := option(cfg); err != nil {
			t.Fatal(err)
		}
	}
	var destination bytes.Buffer
	if err := WithTraceOut(&destination)(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.target.kind != TargetNested || !cfg.selected || !cfg.trace || cfg.traceDelay != 17*time.Millisecond || cfg.traceOut != &destination {
		t.Fatalf("options lost nested mode or trace settings: %+v", cfg)
	}
	for _, option := range []Option{WithNested(target), WithHeadless(target)} {
		if err := option(cfg); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("duplicate managed target accepted: %v", err)
		}
	}
	if err := WithTrace(false)(cfg); err != nil || cfg.trace {
		t.Fatalf("trace opt-out ignored: %v", err)
	}
}

func TestCompositeWaitConditionPreservesEventWakeupsAndShortCircuits(t *testing.T) {
	ctx := context.Background()
	calls := make([]string, 0, 3)
	condition := func(name string, value bool, err error, pollOnly bool) Condition {
		return conditionFunc{name: name, pollOnly: pollOnly, check: func(context.Context, *Session) (bool, error) { calls = append(calls, name); return value, err }}
	}
	observed := errors.New("synthetic unreadable window state")
	falseCondition := condition("false", false, nil, true)
	broken := condition("broken", false, observed, false)
	trueCondition := condition("true", true, nil, true)
	all := All(falseCondition, broken, trueCondition)
	if all.describe() != "all" || all.pollingOnly() {
		t.Fatal("all condition hid event-capable child")
	}
	if value, err := all.evaluate(ctx, nil); value || err != nil || strings.Join(calls, ",") != "false" {
		t.Fatalf("all did not short-circuit unfulfilled proof: value=%v err=%v calls=%v", value, err, calls)
	}
	calls = nil
	any := Any(broken, trueCondition, falseCondition)
	if any.describe() != "any" || any.pollingOnly() {
		t.Fatal("any condition suppressed event wakeups")
	}
	if value, err := any.evaluate(ctx, nil); !value || err != nil || strings.Join(calls, ",") != "broken,true" {
		t.Fatalf("independent positive proof lost: value=%v err=%v calls=%v", value, err, calls)
	}
	calls = nil
	allBroken := Any(broken, falseCondition)
	if value, err := allBroken.evaluate(ctx, nil); value || !errors.Is(err, observed) {
		t.Fatalf("all alternatives unreadable lost error: value=%v err=%v", value, err)
	}
	negative := Not(falseCondition)
	if negative.describe() != "not false" || !negative.pollingOnly() {
		t.Fatal("not condition changed polling classification")
	}
	if value, err := negative.evaluate(ctx, nil); !value || err != nil {
		t.Fatalf("not false result=%v err=%v", value, err)
	}
	if desc := Not(nil).describe(); desc != "not <nil>" {
		t.Fatalf("nil child described as %q", desc)
	}
	if Not(nil).pollingOnly() {
		t.Fatal("nil child claimed polling readiness")
	}
	if _, err := Not(nil).evaluate(ctx, nil); err == nil {
		t.Fatal("nil child condition evaluated as success")
	}
	if !All(trueCondition, falseCondition).pollingOnly() {
		t.Fatal("poll-only composite classified as event-capable")
	}
}

// Runtime setup and PID publication are distinct startup phases. A replaced
// owner PID entry must not redirect startup's write to an unrelated file.
func TestSessionStartupOwnerPIDWriteDoesNotFollowSymlink(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfowner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("TMPDIR", root)
	foreign := filepath.Join(t.TempDir(), "unrelated-user-file")
	const contents = "foreign private content must remain untouched"
	if err := os.WriteFile(foreign, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	var runtimeDir string
	var fixtureErr error
	s := &Session{startupObserver: func(phase StartupPhase, state StartupPhaseState) {
		if phase != StartupPhaseRuntimeDirectoryPreparation || state != StartupPhaseSucceeded {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			fixtureErr = err
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
				continue
			}
			runtimeDir = filepath.Join(root, entry.Name())
			fixtureErr = os.Symlink(foreign, filepath.Join(runtimeDir, sessionOwnerPIDFile))
			return
		}
	}}
	infra, startErr := s.startSession(context.Background(), sessionModeHeadless, SessionConfig{LogDir: filepath.Join(root, "logs")}, false)
	if fixtureErr != nil {
		t.Fatalf("test setup: %v", fixtureErr)
	}
	if runtimeDir == "" {
		t.Fatal("startup never published a managed runtime")
	}
	if infra != nil {
		t.Cleanup(func() { _ = infra.stop() })
	}
	_ = startErr // The installed desktop helper may be unavailable in tests.
	data, err := os.ReadFile(foreign)
	if err != nil || string(data) != contents {
		t.Fatalf("owner PID publication overwrote unrelated file: %q %v", data, err)
	}
}

// A missing caller context must be rejected before any clipboard or keyboard
// backend can dispatch a paste. Context.Background is not an implicit grant.
func TestSessionPasteRejectsNilContextBeforeDispatch(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	if err := session.Paste(nil, "private selection"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("paste without explicit context = %v, want ErrInvalidArgument", err)
	}
}

// An absent or only partially initialized owner never authorizes operations.
func TestSessionAbsentReceiverAndOwnerlessTraceFailClosed(t *testing.T) {
	var session *Session
	if !session.isClosed() {
		t.Fatal("nil session must be treated as closed")
	}
	var base *bundleBase
	base.traceAction("clipboard", "set") // Must not dereference a missing owner.
	orphan := &bundleBase{}
	orphan.traceAction("clipboard", "set")
}

// The direct managed-session stop path must terminate and reap a real helper;
// an interrupted waiter cannot assume ownership of the reap or release the
// process group while it is still running.
func TestManagedSessionDirectStopTerminatesAndReapsActiveHelper(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := sessionruntime.NewSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	if err := sessionruntime.WaitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if process.WaitTimeout(0) {
		t.Fatal("running helper prematurely marked reaped")
	}
	if process.WaitTimeout(10 * time.Millisecond) {
		t.Fatal("running helper completed during timeout")
	}
	process.Stop(250 * time.Millisecond)
	if !process.WaitTimeout(0) {
		t.Fatal("direct stop returned without reaping helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.Wait(ctx, ErrInvalidArgument); err == nil {
		t.Fatal("terminated helper reported successful exit")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("terminated helper still exists: %v", err)
	}
	// Repeated stops must not send signals to a recycled process identity.
	process.Stop(0)
}

func TestManagedSessionWaitPropagatesActualNonzeroExitAndReapsOnce(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 23")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := sessionruntime.NewSessionProcess(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := process.Wait(ctx, ErrInvalidArgument)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("actual child exit status lost: %v", err)
	}
	if !process.WaitTimeout(0) {
		t.Fatal("exit published before reap completed")
	}
	if err2 := process.Wait(ctx, ErrInvalidArgument); !errors.As(err2, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("second waiter did not see same result: %v", err2)
	}
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WNOHANG, nil); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("child not reaped exactly once: %v", err)
	}
}

func TestManagedSessionProcessWaitCancellationKeepsReapingChild(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+readyPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start managed process: %v", err)
	}
	process := sessionruntime.NewSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })

	if err := sessionruntime.WaitForFile(context.Background(), readyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for helper readiness: %v", err)
	}
	if process.WaitTimeout(-time.Second) {
		t.Fatal("negative timeout reported an unfinished child as exited")
	}

	if err := process.Wait(nil, ErrInvalidArgument); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("wait with nil context = %v, want ErrInvalidArgument", err)
	}
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := process.Wait(waitCtx, ErrInvalidArgument); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait with canceled context = %v, want context.Canceled", err)
	}
	if process.WaitTimeout(0) {
		t.Fatal("canceled waiter released ownership before the child was reaped")
	}

	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("terminate managed process group: %v", err)
	}
	completionCtx, completionCancel := context.WithTimeout(context.Background(), time.Second)
	defer completionCancel()
	if err := process.Wait(completionCtx, ErrInvalidArgument); err == nil {
		t.Fatal("reaped helper process unexpectedly returned a successful exit")
	}
	if !process.WaitTimeout(0) {
		t.Fatal("managed process did not publish completed wait")
	}
}

// A helper that deliberately ignores SIGTERM must still be killed and reaped
// by the direct managed-session shutdown path.
func TestManagedSessionStopEscalatesAfterIgnoredSIGTERM(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready, "GO_WANT_IGNORE_SIGTERM=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := sessionruntime.NewSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	if err := sessionruntime.WaitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// The helper has installed the ignore handler before reporting ready.
	if _, err := os.Stat(ready); err != nil {
		t.Fatal(err)
	}
	process.Stop(30 * time.Millisecond)
	if !process.WaitTimeout(0) {
		t.Fatal("stop returned without reaping SIGTERM-resistant helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.Wait(ctx, ErrInvalidArgument); err == nil {
		t.Fatal("forced termination reported success")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("helper still exists after stop escalation: %v", err)
	}
	process.Stop(0)
}
