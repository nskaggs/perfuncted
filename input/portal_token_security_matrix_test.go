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
			if lintShadow35 := os.WriteFile(tokenPath, []byte(tc.content), tc.mode); lintShadow35 != nil {
				t.Fatal(lintShadow35)
			}
			if lintShadow38 := os.Chmod(tokenPath, tc.mode); lintShadow38 != nil {
				t.Fatal(lintShadow38)
			}
			if tc.hardlink {
				outside := filepath.Join(t.TempDir(), "external-restore-record")
				if lintShadow43 := os.Link(tokenPath, outside); lintShadow43 != nil {
					t.Fatal(lintShadow43)
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
	if lintShadow62 := lock.store(value); lintShadow62 != nil {
		t.Fatalf("maximum valid token rejected: %v", lintShadow62)
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
	if _, lintShadow77 := os.Stat(path); !os.IsNotExist(lintShadow77) {
		t.Fatalf("single-use record not deleted: %v", lintShadow77)
	}
	if lintShadow80 := lock.Close(); lintShadow80 != nil {
		t.Fatal(lintShadow80)
	}
	if lintShadow83 := lock.Close(); lintShadow83 != nil {
		t.Fatalf("idempotent close: %v", lintShadow83)
	}
	if lintShadow86 := lock.store("never-written"); lintShadow86 == nil {
		t.Fatal("closed store accepted a mutation")
	}
	if got, lintShadow89 := lock.consume(); lintShadow89 == nil || got != "" {
		t.Fatalf("closed store accepted a read %q %v", got, lintShadow89)
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
			if lintShadow121 := lock.Close(); lintShadow121 != nil {
				t.Fatal(lintShadow121)
			}
			dir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
			if tc.name == "lock file symlink" {
				if lintShadow126 := os.Remove(filepath.Join(dir, portalRestoreLockFile)); lintShadow126 != nil {
					t.Fatal(lintShadow126)
				}
			}
			if lintShadow130 := tc.setup(dir); lintShadow130 != nil {
				t.Fatal(lintShadow130)
			}
			// A previous private lock file exists; in the world-readable scenario
			// make its actual mode insecure rather than O_CREAT simply preserving it.
			if tc.name == "world-readable lock file" {
				if lintShadow136 := os.Chmod(filepath.Join(dir, portalRestoreLockFile), 0644); lintShadow136 != nil {
					t.Fatal(lintShadow136)
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
