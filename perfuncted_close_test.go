package perfuncted

import (
	"os"
	"path/filepath"
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
