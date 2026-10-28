package perfuncted

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSessionCloseCleansOwnedInfrastructure(t *testing.T) {
	xdgDir := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdgDir, 0o700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	session.infra = &sessionInfra{xdgDir: xdgDir}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(xdgDir); !os.IsNotExist(err) {
		t.Fatal("owned session infrastructure was not cleaned")
	}
}

func TestSessionCloseIsConcurrentAndIdempotent(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	var waitGroup sync.WaitGroup
	for range 16 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := session.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}
	waitGroup.Wait()
}

func TestSessionCloseNil(t *testing.T) {
	var session *Session
	if err := session.Close(); err != nil {
		t.Fatalf("nil Close returned error: %v", err)
	}
}

// A rejected runtime cleanup is not a successful session teardown. Every
// caller of Close must observe the same failure even when shutdown joins a
// concurrent signal-triggered stop.
func TestSessionCloseReportsUnsafeRuntimeCleanup(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	session.infra = &sessionInfra{xdgDir: "/"}
	err := session.Close()
	if err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("Close error=%v, want unsafe runtime cleanup failure", err)
	}
	if again := session.Close(); again != err {
		t.Fatalf("Close replay error=%v, want original error=%v", again, err)
	}
}
