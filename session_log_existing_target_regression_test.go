package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
			file, err := openPrivateSessionLog(logPath)
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
