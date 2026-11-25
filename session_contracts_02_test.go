package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// CleanupSessionLogs is an exported maintenance entry point. Unlike normal
// session startup, callers can supply a symlink root: cleanup must not follow
// it into a separate tree and delete unrelated directories there.
func TestCleanupSessionLogsNeverFollowsSymlinkRoot(t *testing.T) {
	base := t.TempDir()
	external := filepath.Join(base, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(external, sessionruntime.SessionLogPrefix+"expired")
	if err := os.Mkdir(victim, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("do-not-delete"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "log-alias")
	if err := os.Symlink(external, alias); err != nil {
		t.Fatal(err)
	}
	CleanupSessionLogs(alias, 24*time.Hour)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("followed symlink root and deleted external log: %v", err)
	}
}

// A session creates its own fresh log directory and must never truncate an
// existing path, including a symlink or a hard-linked file, even if its log
// directory was unexpectedly tampered with.
func TestSessionLogCreationNeverTruncatesPreexistingTargets(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "regular"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "private-data")
			const original = "session-private-content-must-not-be-lost"
			if err := os.WriteFile(target, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(dir, "sway-session.log")
			switch mode {
			case "symlink":
				if err := os.Symlink(target, logPath); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, logPath); err != nil {
					t.Fatal(err)
				}
			case "regular":
				if err := os.WriteFile(logPath, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
			}
			file, err := sessionruntime.OpenPrivateSessionLog(logPath)
			if file != nil {
				_ = file.Close()
			}
			if err == nil {
				t.Fatal("reused existing log path instead of failing closed")
			}
			got, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != original {
				t.Fatalf("existing target truncated/modified: got %q", got)
			}
		})
	}
}

func TestRejectedSwayStartupDoesNotPoisonFreshLogDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	infra := &sessionInfra{logDir: dir, ctx: context.Background()}
	logPath := filepath.Join(dir, "sway-session.log")
	for _, mode := range []sessionMode{sessionMode(12345), sessionModeNested} {
		if err := infra.launchSway(context.Background(), filepath.Join(dir, "unused.conf"), mode); err == nil {
			t.Fatalf("unexpected success for invalid/unavailable mode %v", mode)
		}
		if _, err := os.Lstat(logPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected mode %v created/modified log path: %v", mode, err)
		}
	}
}

// A managed log root may become unreadable while retaining private session
// directories. Retention must not infer that those directories are absent,
// follow a fallback path, or modify an unrelated root.
func TestSessionLogRetentionDoesNotTouchUnreadableRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory read permission")
	}
	root := filepath.Join(t.TempDir(), "private-retention")
	child := filepath.Join(root, sessionruntime.SessionLogPrefix+"retained")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(child, "private.log")
	if err := os.WriteFile(payload, []byte("preserve private audit"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(child, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(root, 0700); err != nil {
			t.Errorf("restore log root permissions: %v", err)
		}
	})
	CleanupSessionLogs(root, time.Hour)
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(payload)
	if err != nil || string(got) != "preserve private audit" {
		t.Fatalf("unreadable retention root lost audit data: got=%q err=%v", got, err)
	}
}

// The raw sessionruntime.Group path owns process-group shutdown without an asynchronous
// reaper. A leader that ignores SIGTERM must be force-killed and reaped, not
// reported stopped merely because the TERM grace elapsed.
func TestManagedProcDirectStopEscalatesAndReapsTermResistantLeader(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready, "GO_WANT_IGNORE_SIGTERM=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = cmd.Wait() })
	if err := sessionruntime.WaitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	proc := sessionruntime.NewGroup(cmd, pid)
	if !proc.GroupAlive() {
		t.Fatal("running helper group was not detected")
	}
	proc.Stop(60 * time.Millisecond)
	if proc.GroupAlive() || sessionruntime.ProcessGroupAlive(pid) {
		t.Fatal("SIGTERM-resistant helper survived direct stop escalation")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("direct stop failed to reap helper pid=%d: %v", pid, err)
	}
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("direct stop left waitable process: %v", err)
	}
	// Repeated stop must not signal an unrelated process that inherited the PID.
	proc.Stop(0)
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("signal after reap: %v", err)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("helper never initialized handler: %v", err)
	}
}

// A nil parent context is a supported call path and must be replaced by a
// bounded background context rather than passed to context.WithTimeout.
func TestCapabilityOpeningNilParentStillUsesBoundedContext(t *testing.T) {
	backend := &trackedCapabilityBackend{}
	installed := false
	got, err := openCapabilityBackend[*trackedCapabilityBackend](nil, time.Second, CapabilityScreen,
		func(ctx context.Context) (*trackedCapabilityBackend, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 {
				return nil, errors.New("nil parent did not receive a live startup deadline")
			}
			return backend, nil
		}, nil,
		func(value *trackedCapabilityBackend) { installed = value == backend },
	)
	if err != nil || got != backend || !installed || backend.closes != 0 {
		t.Fatalf("nil-parent capability result %v, err=%v installed=%v closes=%d", got, err, installed, backend.closes)
	}
}

// Capability setup can be called before a screen backend exists. No timeout
// mutation may be attempted on a missing backend.
func TestConfigureScreenTimeoutAcceptsMissingBackend(t *testing.T) {
	configureScreenBackendTimeout(nil, time.Second)
}

// PID 0 and negative IDs must never be used as liveness evidence: kill(0,0)
// targets a whole process group and has different authorization semantics.
func TestSessionPIDLivenessRejectsNonpositiveIdentifiers(t *testing.T) {
	for _, pid := range []int{0, -1, -1024} {
		if sessionruntime.PIDAlive(pid) {
			t.Fatalf("invalid process identifier %d accepted as live", pid)
		}
	}
}

// A missing clipboard facade must not initiate a desktop write.
func TestNilClipboardFacadeCannotWrite(t *testing.T) {
	var clipboard *ClipboardBundle
	if err := clipboard.Set(context.Background(), "private text"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil clipboard Set error %v, want ErrNilSession", err)
	}
}

// Optional nil configuration entries must not prevent a host session from
// opening, and explicitly enabled action traces must use the caller's writer.
func TestOpenIgnoresNilOptionAndRoutesOwnedTrace(t *testing.T) {
	var writer bytes.Buffer
	session, err := Open(context.Background(), nil, WithTrace(true), WithTraceOut(&writer))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.tracer == nil {
		t.Fatal("requested trace writer did not initialize")
	}
	session.Input.traceAction("input", "key-up %s", "Return")
	if got := writer.String(); !strings.Contains(got, "input key-up Return") {
		t.Fatalf("session did not route trace to configured writer: %q", got)
	}
	if session.Has(CapabilityInput) {
		t.Fatal("an unrequested input backend became available")
	}
}

// An invalid context must be refused before configuration or desktop routing.
func TestOpenNilContextDoesNotEvaluateOptions(t *testing.T) {
	called := false
	session, err := Open(nil, Option(func(*openConfig) error {
		called = true
		return nil
	}))
	if session != nil || !errors.Is(err, ErrInvalidArgument) || called {
		t.Fatalf("nil-context Open returned session=%v err=%v evaluatedOptions=%v", session, err, called)
	}
}

// Invalid configuration must stop option evaluation before any runtime or
// backend setup; a later option cannot turn an error into success.
func TestOpenOptionFailureStopsConfigurationBeforeRuntime(t *testing.T) {
	rejected := errors.New("synthetic rejected desktop configuration")
	laterCalled := false
	session, err := Open(context.Background(),
		Option(func(*openConfig) error { return rejected }),
		Option(func(*openConfig) error { laterCalled = true; return nil }),
	)
	if session != nil || !errors.Is(err, rejected) || laterCalled {
		t.Fatalf("failed option opened session=%v err=%v laterCalled=%v", session, err, laterCalled)
	}
}

// A configuration option can end the owner's permission to start a desktop.
func TestOpenOptionCancellationStopsRuntimeSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := Open(ctx, Option(func(*openConfig) error {
		cancel()
		return nil
	}))
	if session != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("configuration cancellation opened session=%v err=%v", session, err)
	}
}

// Closing a session revokes direct paste authority independently of whether
// clipboard and keyboard backends were ever requested.
func TestClosedSessionPasteRejectsWithoutDesktopDispatch(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Paste(context.Background(), "private selection"); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session accepted paste: %v", err)
	}
}
