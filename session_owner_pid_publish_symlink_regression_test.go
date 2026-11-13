package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
