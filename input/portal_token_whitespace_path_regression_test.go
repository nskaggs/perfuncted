//go:build linux

package input

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Unix directory names may begin or end in spaces. Treat an explicitly
// provided state path as an exact identity, not as user-entered prose.
func TestPortalRestoreTokenStorePreservesWhitespaceInStateAndHomePaths(t *testing.T) {
	for _, tc := range []struct {
		name, variable string
		suffix         []string
	}{
		{"XDG_STATE_HOME", "XDG_STATE_HOME", []string{"perfuncted"}},
		{"HOME fallback", "HOME", []string{".local", "state", "perfuncted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := privatePortalTokenFixtureDir(t)
			statePath := filepath.Join(root, " state ")
			if err := os.Mkdir(statePath, 0700); err != nil {
				t.Fatal(err)
			}
			rt := env.FromEnviron([]string{tc.variable + "=" + statePath})
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			intended := filepath.Join(append([]string{statePath}, tc.suffix...)...)
			if _, err := os.Stat(intended); err != nil {
				t.Fatalf("store did not use exact %s directory %q: %v", tc.variable, intended, err)
			}
			if err := lock.store("secret-restoration-token"); err != nil {
				t.Fatal(err)
			}
			if token, err := lock.consume(); err != nil || token != "secret-restoration-token" {
				t.Fatalf("wrong token: %q %v", token, err)
			}
		})
	}
}
