//go:build linux

package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortalTokenRejectsUntrustedOnDiskRestoreRecords(t *testing.T) {
	for _, tc := range []struct {
		name     string
		content  string
		mode     os.FileMode
		hardlink bool
	}{
		{"zero-length token", "", 0600, false},
		{"missing owner privacy", "stored\n", 0644, false},
		{"embedded nul", "evil\x00token\n", 0600, false},
		{"embedded newline", "first\nsecond\n", 0600, false},
		{"carriage return", "before\r\n", 0600, false},
		{"oversized record", strings.Repeat("t", 4098), 0600, false},
		{"multiple hard links", "external-token\n", 0600, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := portalTokenTestRuntime(t)
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			tokenPath := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
			if err := os.WriteFile(tokenPath, []byte(tc.content), tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(tokenPath, tc.mode); err != nil {
				t.Fatal(err)
			}
			if tc.hardlink {
				outside := filepath.Join(t.TempDir(), "external-restore-record")
				if err := os.Link(tokenPath, outside); err != nil {
					t.Fatal(err)
				}
			}
			token, err := lock.consume()
			if err == nil || token != "" {
				t.Fatalf("untrusted on-disk token accepted: token=%q error=%v", token, err)
			}
		})
	}
}

func TestPortalTokenSingleUsePersistsOnlyPrivateRecordAndRejectsClosedStore(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("x", 4096)
	if err := lock.store(value); err != nil {
		t.Fatalf("maximum valid token rejected: %v", err)
	}
	path := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 || info.Size() != 4097 {
		t.Fatalf("private stored record perms=%o size=%d", info.Mode().Perm(), info.Size())
	}
	got, err := lock.consume()
	if err != nil || got != value {
		t.Fatalf("maximum valid token changed: len=%d err=%v", len(got), err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("single-use record not deleted: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if err := lock.store("never-written"); err == nil {
		t.Fatal("closed store accepted a mutation")
	}
	if got, err := lock.consume(); err == nil || got != "" {
		t.Fatalf("closed store accepted a read %q %v", got, err)
	}
	reopened, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatalf("reopen after release: %v", err)
	}
	defer reopened.Close()
	if got, err := reopened.consume(); err != nil || got != "" {
		t.Fatalf("reopened state leaked stale token %q %v", got, err)
	}
}

func TestPortalTokenRejectsUnsafeLockFileAndStateDirectory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(string) error
	}{
		{"world-readable lock file", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, portalRestoreLockFile), []byte(""), 0644)
		}},
		{"world-readable application dir", func(dir string) error { return os.Chmod(dir, 0755) }},
		{"lock file symlink", func(dir string) error {
			return os.Symlink(filepath.Join(dir, "outside"), filepath.Join(dir, portalRestoreLockFile))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := portalTokenTestRuntime(t)
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
			if tc.name == "lock file symlink" {
				if err := os.Remove(filepath.Join(dir, portalRestoreLockFile)); err != nil {
					t.Fatal(err)
				}
			}
			if err := tc.setup(dir); err != nil {
				t.Fatal(err)
			}
			// A previous private lock file exists; in the world-readable scenario
			// make its actual mode insecure rather than O_CREAT simply preserving it.
			if tc.name == "world-readable lock file" {
				if err := os.Chmod(filepath.Join(dir, portalRestoreLockFile), 0644); err != nil {
					t.Fatal(err)
				}
			}
			reopened, err := lockPortalTokenStore(rt)
			if reopened != nil {
				_ = reopened.Close()
			}
			if err == nil {
				t.Fatalf("unsafe state root unexpectedly accepted")
			}
		})
	}
}
