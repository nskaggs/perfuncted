package perfuncted

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Normal session shutdown must not delete a runtime tree that another account
// can modify or has replaced, even if its path retains the managed prefix.
func TestSessionShutdownRefusesOtherWritableRuntimeReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name   string
		mode   os.FileMode
		remove bool
	}{
		{"private", 0700, true},
		{"group-writable", 0770, false},
		{"world-writable", 0777, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "perfuncted-xdg-"+tc.name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			marker := filepath.Join(dir, "foreign-marker")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			infra := &sessionInfra{xdgDir: dir}
			err := infra.stop()
			_, statErr := os.Stat(marker)
			if tc.remove {
				if err != nil || !os.IsNotExist(statErr) {
					t.Fatalf("private runtime shutdown: err=%v marker=%v", err, statErr)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "unsafe") || statErr != nil {
					t.Fatalf("untrusted runtime removed or accepted: err=%v marker=%v", err, statErr)
				}
			}
		})
	}
}
