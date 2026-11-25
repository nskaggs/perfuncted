package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/window"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A completed application and a canceled caller are both ready in the select.
// Cancellation must have priority so an already-canceled automation request
// does not randomly claim that waiting completed successfully.
func TestApplicationWaitCompletedProcessDoesNotOverrideCanceledCaller(t *testing.T) {
	completed := make(chan struct{})
	close(completed)
	app := &Application{done: completed}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		if err := app.Wait(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Wait returned %v on completed process at attempt %d", err, i)
		}
	}
}

func TestApplicationWaitCompletedProcessPreservesExitAndDeadlineErrors(t *testing.T) {
	done := make(chan struct{})
	close(done)
	failure := errors.New("synthetic process failed")
	app := &Application{done: done, err: failure, path: "/synthetic/app"}
	if err := app.Wait(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("completed process exit error changed: %v", err)
	}
	if app.PID() != 0 || app.Path() != "/synthetic/app" || !app.Exited() {
		t.Fatal("completed process metadata wrong")
	}
	deadlineCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := app.Wait(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired wait context returned %v", err)
	}
	if err := app.Wait(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil Wait context error=%v", err)
	}
	var missing *Application
	if missing.Path() != "" || !missing.Exited() || missing.PID() != 0 || missing.Wait(context.Background()) != nil || missing.Stop(context.Background()) != nil || missing.Kill() != nil {
		t.Fatal("nil application failed safe lifecycle contract")
	}
}

// Startup error paths must not register an application, start a process group,
// or silently downgrade invalid/canceled requests into a successful launch.
func TestSessionLaunchRejectsInvalidOrUnavailableCommandsWithoutTrackingProcesses(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		command Command
		match   error
	}{
		{"nil context", nil, Command{Name: "sh"}, ErrInvalidArgument},
		{"canceled context", canceled, Command{Name: "sh"}, context.Canceled},
		{"empty command", context.Background(), Command{}, ErrInvalidArgument},
		{"missing executable", context.Background(), Command{Name: "pf-nonexistent-executable-do-not-create"}, exec.ErrNotFound},
		{"missing child directory", context.Background(), Command{Name: os.Args[0], Dir: filepath.Join(t.TempDir(), "absent")}, os.ErrNotExist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(session.apps)
			app, err := session.Launch(tc.ctx, tc.command)
			if app != nil || !errors.Is(err, tc.match) {
				t.Fatalf("invalid launch returned application=%v error=%v, want %v", app, err, tc.match)
			}
			if len(session.apps) != before {
				t.Fatalf("invalid launch registered application: before=%d after=%d", before, len(session.apps))
			}
		})
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if app, err := session.Launch(context.Background(), Command{Name: os.Args[0]}); app != nil || !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session launched process: app=%v err=%v", app, err)
	}
}

func TestApplicationPathTracksResolvedSyntheticExecutable(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	executable := os.Args[0]
	var childStderr bytes.Buffer
	// The nested test binary is independently instrumented when the parent
	// uses -coverprofile. Give it its own writable coverage directory rather
	// than reusing the Go command's quota-limited shared /tmp coverage path.
	coverageDir := t.TempDir()
	childEnv := append(os.Environ(), "GOCOVERDIR="+coverageDir, "TMPDIR="+coverageDir)
	app, err := session.Launch(context.Background(), Command{Name: executable, Args: []string{"-test.run=^TestSyntheticApplicationChildExit$"}, Env: childEnv, Stderr: &childStderr})
	if err != nil {
		t.Fatal(err)
	}
	if got := app.Path(); got == "" || !strings.Contains(got, filepath.Base(executable)) {
		t.Fatalf("application did not record actual executable: %q", got)
	}
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("synthetic child process failed: %v; stderr=%q", err, childStderr.String())
	}
}

func TestSyntheticApplicationChildExit(t *testing.T) {}

// A caller that stops a managed process directly must escalate from TERM to
// KILL when the process ignores TERM. Do not require Session.Close to perform
// escalation or report the expected forced exit as a shutdown failure.
func TestStopApplicationEscalatesIgnoredTERMAndReapsProcess(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	session.config.ApplicationGracePeriod = 80 * time.Millisecond
	t.Cleanup(func() { _ = session.Close() })

	ready := filepath.Join(t.TempDir(), "ready")
	app, err := session.Launch(context.Background(), Command{
		Name: "sh",
		Args: []string{"-c", `trap '' TERM; : > "$PF_READY"; while :; do :; done`},
		Env:  []string{"PF_READY=" + ready},
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	// Ensure the child has installed its TERM handler before sending a signal.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatalf("read ready marker: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("child never installed its TERM handler")
		}
		time.Sleep(time.Millisecond)
	}

	if err := session.stopApplication(app); err != nil {
		t.Fatalf("stop application: %v", err)
	}
	if app.proc.GroupAlive() {
		t.Fatal("TERM-resistant application process group survived escalation")
	}
	if !app.Exited() {
		t.Fatal("process leader not reaped after escalation")
	}
	// Repeated stop cannot signal a newly allocated process group with the
	// reused numeric PID after the original process has already terminated.
	if err := session.stopApplication(app); err != nil {
		t.Fatalf("repeat stop: %v", err)
	}
}

// Without PID attribution, two matching windows are not proof that either
// belongs to the application. A transport failure likewise cannot authorize
// a window handle; both cases may recover when observation becomes precise.
func TestApplicationWindowWaitDoesNotAcceptAmbiguousOrUnreadableCandidates(t *testing.T) {
	manager := newHandleWindowManager(
		window.Info{NativeID: "first", Title: "Browser"},
		window.Info{NativeID: "second", Title: "Browser"},
	)
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	app := &Application{session: session, done: make(chan struct{})}
	match := WindowMatch{TitleExact: "Browser"}
	for _, tc := range []struct {
		name    string
		failure error
	}{
		{name: "ambiguous candidates"},
		{name: "unreadable candidates", failure: errors.New("synthetic desktop backend offline")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager.setError(tc.failure)
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			found, err := app.WaitForWindow(ctx, match)
			if found != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("ambiguous or unreadable candidates accepted: window=%v err=%v", found, err)
			}
		})
	}
	manager.setError(nil)
	manager.setWindows(false, window.Info{NativeID: "first", Title: "Browser"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	found, err := app.WaitForWindow(ctx, match)
	if err != nil || found == nil || found.ID().String() != "first" {
		t.Fatalf("unique candidate not accepted after recovery: window=%v err=%v", found, err)
	}
}
