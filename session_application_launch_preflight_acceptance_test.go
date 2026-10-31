package perfuncted

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	app, err := session.Launch(context.Background(), Command{Name: executable, Args: []string{"-test.run=^TestSyntheticApplicationChildExit$"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := app.Path(); got == "" || !strings.Contains(got, filepath.Base(executable)) {
		t.Fatalf("application did not record actual executable: %q", got)
	}
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("synthetic child process failed: %v", err)
	}
}

func TestSyntheticApplicationChildExit(t *testing.T) {}
