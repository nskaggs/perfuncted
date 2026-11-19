//go:build linux

package perfuncted

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Retention must never claim success deleting an expired owned log directory
// if its caller-owned parent becomes unreadable or unwritable. Once ownership
// and permissions return, the original expired directory remains eligible.
func TestSessionLogRetentionPreservesExpiredSessionsWhileRootAccessDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can bypass directory permissions")
	}
	for _, access := range []struct {
		name string
		mode os.FileMode
	}{
		{"cannot-read-root", 0o000},
		{"cannot-remove-child", 0o500},
	} {
		t.Run(access.name, func(t *testing.T) {
			root := t.TempDir()
			expired := filepath.Join(root, sessionLogPrefix+"expired")
			if err := os.Mkdir(expired, 0o700); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-72 * time.Hour)
			if err := os.Chtimes(expired, old, old); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(root, access.mode); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chmod(root, 0o700) }()
			CleanupSessionLogs(root, 24*time.Hour)
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(expired); err != nil {
				t.Fatalf("denied retention removed expired log: %v", err)
			}
			CleanupSessionLogs(root, 24*time.Hour)
			if _, err := os.Lstat(expired); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expired session survived after permissions restored: %v", err)
			}
		})
	}
}
