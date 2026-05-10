package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPerfunctedCloseCleansManagedSession(t *testing.T) {
	xdgDir := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdgDir, 0o700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}

	sess := &Session{xdgDir: xdgDir}
	pf := &Perfuncted{managed: sess}

	if err := pf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sess.IsCleaned() {
		t.Fatal("managed session was not cleaned")
	}
}
