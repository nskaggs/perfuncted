//go:build linux

package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The store always writes token + '\n' and caps the token at 4096 bytes.
// A partially written or tampered record must not be passed to the portal
// as a new authorization credential, even when it is otherwise valid UTF-8.
func TestPortalTokenRejectsIncompleteAndOversizedUnterminatedRecords(t *testing.T) {
	for _, tc := range []struct{ name, record string }{
		{"missing terminator", "otherwise-valid-token"},
		{"maximum size without newline", strings.Repeat("A", 4097)},
		{"trailing additional newline", "token\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := portalTokenTestRuntime(t)
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			path := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
			if err := os.WriteFile(path, []byte(tc.record), 0600); err != nil {
				t.Fatal(err)
			}
			token, err := lock.consume()
			if err == nil || token != "" {
				t.Fatalf("corrupted restore-token record granted authorization: token=%q err=%v", token, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("corrupted token remained after read: %v", err)
			}
		})
	}
}
