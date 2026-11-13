package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
)

// Cleanup PID markers authorize process control. A file writable by another
// account cannot prove that its recorded PID belongs to this session, even
// when it is a regular file with one link and valid numeric contents.
func TestSessionCleanupRejectsOtherWritablePIDMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dbus.pid")
	for _, tc := range []struct {
		name   string
		mode   os.FileMode
		accept bool
	}{
		{"owner-private", 0600, true},
		{"owner-readable", 0644, true},
		{"group-writable", 0620, false},
		{"world-writable", 0602, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte("12345\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			pid, err := readPIDFile(path)
			if tc.accept && (err != nil || pid != 12345) {
				t.Fatalf("valid PID rejected: %d %v", pid, err)
			}
			if !tc.accept && (err == nil || pid != 0) {
				t.Fatalf("untrusted writable PID accepted: %d %v", pid, err)
			}
		})
	}
}
