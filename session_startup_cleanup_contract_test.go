package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A startup error and its cleanup error are both authoritative. The caller
// must not be told only about the launch failure while a managed runtime
// remains on disk and may still have resources attached.
func TestStartSessionFailureReportsCleanupError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires ordinary filesystem permissions")
	}
	root, err := os.MkdirTemp(os.TempDir(), "pft-")
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
