package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/window"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A completed application and a canceled caller are both ready in the select.
// Cancellation must have priority so an already-canceled automation request
// does not randomly claim that waiting completed successfully.
func TestApplicationWaitCompletedProcessDoesNotOverrideCanceledCaller(t *testing.T) {
	completed := make(chan struct{})
	close(completed)
	app := &applicationState{done: completed}
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
	app := &applicationState{done: done, err: failure, path: "/synthetic/app"}
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
	var missing *applicationState
	if missing.Path() != "" || !missing.Exited() || missing.PID() != 0 || missing.Wait(context.Background()) != nil || missing.Stop(context.Background()) != nil || missing.Kill() != nil {
		t.Fatal("nil application failed safe lifecycle contract")
	}
}

// Startup error paths must not register an application, start a process group,
// or silently downgrade invalid/canceled requests into a successful launch.
func TestSessionLaunchRejectsInvalidOrUnavailableCommandsWithoutTrackingProcesses(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	app := &applicationState{session: session, done: make(chan struct{})}
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

type notifiedLaunchContext struct {
	context.Context
	checked chan struct{}
	once    sync.Once
}

func (c *notifiedLaunchContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

// A session launch can pass its initial context preflight and then wait for
// lifecycle ownership. Cancellation during that wait must prohibit spawning
// the executable even if it is otherwise valid and available.
func TestSessionLaunchRejectsCancellationWhileWaitingForLifecycleOwnership(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	marker := filepath.Join(t.TempDir(), "unexpected-child")
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &notifiedLaunchContext{Context: parent, checked: make(chan struct{})}
	session.lifecycleMu.Lock()
	lockHeld := true
	defer func() {
		if lockHeld {
			session.lifecycleMu.Unlock()
		}
	}()
	result := make(chan error, 1)
	go func() {
		app, err := session.Launch(ctx, Command{Name: "sh", Args: []string{"-c", "printf started > " + marker}})
		if app != nil {
			result <- errors.New("canceled launch returned a child")
			return
		}
		result <- err
	}()
	select {
	case <-ctx.checked:
	case <-time.After(2 * time.Second):
		t.Fatal("launch never passed its initial context preflight")
	}
	cancel()
	session.lifecycleMu.Unlock()
	lockHeld = false
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceling launch did not reject executable: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled launch remained blocked on lifecycle lock")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("executable started despite revoked authorization: %v", err)
	}
	if len(session.apps) != 0 {
		t.Fatalf("canceled launch retained %d children", len(session.apps))
	}
}

// Returning a completed child is not authority to report success if the
// caller's cancellation arrives between wait preflight and result admission.
func TestApplicationWaitRechecksRevocationAfterChildCompletion(t *testing.T) {
	done := make(chan struct{})
	close(done)
	app := &applicationState{done: done}
	for attempt := 0; attempt < 100; attempt++ {
		base, cancel := context.WithCancelCause(context.Background())
		ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
		err := app.Wait(ctx)
		if !errors.Is(err, context.Canceled) || ctx.calls != 2 {
			t.Fatalf("late cancellation after child completion: err=%v checks=%d", err, ctx.calls)
		}
		cancel(nil)
	}
}

type cancelWhenWaitSelectsContext struct {
	context.Context
	cancel context.CancelFunc
}

func (c *cancelWhenWaitSelectsContext) Done() <-chan struct{} {
	c.cancel()
	return c.Context.Done()
}

// Revocation after the first preflight, while waiting for a live child, must
// complete the wait without requiring that the child exit.
func TestApplicationWaitRevokedDuringLiveChildObservation(t *testing.T) {
	app := &applicationState{done: make(chan struct{})}
	for attempt := 0; attempt < 100; attempt++ {
		base, cancel := context.WithCancel(context.Background())
		ctx := &cancelWhenWaitSelectsContext{Context: base, cancel: cancel}
		if err := app.Wait(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("live child wait continued after owner cancellation: %v", err)
		}
		select {
		case <-app.done:
			t.Fatal("canceling wait altered child completion")
		default:
		}
	}
}
