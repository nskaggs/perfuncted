package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A matching runtime name is not an ownership grant. Reaping a directory
// writable by untrusted accounts can delete files they contributed or modified.
func TestStaleCleanupRejectsNonprivateRuntimeDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name   string
		mode   os.FileMode
		remove bool
	}{
		{"private-runtime", 0700, true},
		{"group-writable-runtime", 0770, false},
		{"world-writable-runtime", 0777, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "perfuncted-xdg-"+tc.name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			marker := filepath.Join(dir, "private-payload")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDir(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(marker)
			if tc.remove && !os.IsNotExist(err) {
				t.Fatalf("private stale runtime not reaped: %v", err)
			}
			if !tc.remove && err != nil {
				t.Fatalf("untrusted writable runtime deleted: %v", err)
			}
		})
	}
}
