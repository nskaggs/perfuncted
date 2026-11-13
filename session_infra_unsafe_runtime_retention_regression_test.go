package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Session shutdown never owns an arbitrary caller directory. Even a path
// shaped like a managed runtime must be retained if its ownership or mode is
// inconsistent with a private per-session directory.
func TestManagedSessionShutdownRejectsForeignOrUntrustedRuntimeDirectories(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime func(*testing.T) string
	}{
		{"foreign directory", func(t *testing.T) string { return t.TempDir() }},
		{"nonprivate managed directory", func(t *testing.T) string {
			dir, err := createManagedSessionRuntimeDir()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			if err := os.Chmod(dir, 0777); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.runtime(t)
			activeRoot := filepath.Join(t.TempDir(), "active-temp-root")
			if err := os.Mkdir(activeRoot, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", activeRoot)
			payload := filepath.Join(dir, "foreign-data")
			if err := os.WriteFile(payload, []byte("preserve me"), 0600); err != nil {
				t.Fatal(err)
			}
			infra := &sessionInfra{xdgDir: dir}
			first := infra.stop()
			second := infra.stop()
			if first == nil || !strings.Contains(first.Error(), "refuse unsafe managed runtime") || second == nil || second.Error() != first.Error() {
				t.Fatalf("unsafe runtime removed or shutdown error unstable: first=%v second=%v", first, second)
			}
			data, err := os.ReadFile(payload)
			if err != nil || string(data) != "preserve me" {
				t.Fatalf("shutdown touched foreign runtime content: data=%q err=%v", data, err)
			}
		})
	}
}

// A failed log setup occurs before sessionInfra is constructed, but the
// runtime was already allocated. The startup transaction must remove that
// directory rather than leak its private socket namespace.
func TestManagedSessionLogCreationFailureRollsBackAllocatedRuntime(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pf-rollback-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("TMPDIR", root)
	blocker := filepath.Join(root, "blocking-file")
	if err := os.WriteFile(blocker, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	session := &Session{}
	infra, startErr := session.startSession(context.Background(), sessionModeHeadless, SessionConfig{LogDir: filepath.Join(blocker, "logs")}, false)
	if infra != nil || startErr == nil || !strings.Contains(startErr.Error(), "create logs") {
		t.Fatalf("invalid log directory admitted: infra=%v err=%v", infra, startErr)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
			t.Fatalf("failed startup leaked managed runtime %q", entry.Name())
		}
	}
	if data, err := os.ReadFile(blocker); err != nil || string(data) != "retain" {
		t.Fatalf("startup altered foreign log blocker: %q %v", data, err)
	}
	if errors.Is(startErr, context.Canceled) {
		t.Fatalf("log filesystem failure misreported as cancellation: %v", startErr)
	}
}
