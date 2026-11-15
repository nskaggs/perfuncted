//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// An existing regular file in the GNOME extension directory hierarchy is
// not an installation directory. Refuse provisioning without replacing that
// file, creating staging directories, or changing GNOME settings.
func TestGNOMEInstallerRejectsRegularFileDirectoryAncestorsWithoutMutation(t *testing.T) {
	for _, where := range []string{"gnome-shell", "extensions"} {
		t.Run(where, func(t *testing.T) {
			home := t.TempDir()
			ancestor := filepath.Join(home, "gnome-shell")
			if where == "extensions" {
				if err := os.Mkdir(ancestor, 0700); err != nil {
					t.Fatal(err)
				}
				ancestor = filepath.Join(ancestor, "extensions")
			}
			const marker = "existing user-owned content"
			if err := os.WriteFile(ancestor, []byte(marker), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			i := Installer{DataHome: home, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) { calls++; return nil, nil }}
			destination, err := i.Install(context.Background(), env.FromEnviron(nil))
			if destination != "" || !errors.Is(err, os.ErrExist) && (err == nil || !strings.Contains(err.Error(), "not a directory")) {
				t.Fatalf("non-directory ancestor accepted: destination=%q err=%v", destination, err)
			}
			if calls != 0 {
				t.Fatalf("denied install changed GNOME settings %d times", calls)
			}
			got, err := os.ReadFile(ancestor)
			if err != nil || string(got) != marker {
				t.Fatalf("original file changed: %q %v", got, err)
			}
			if entries, err := os.ReadDir(filepath.Dir(ancestor)); err != nil || len(entries) != 1 {
				t.Fatalf("failed installation created staging artifacts: %v %v", entries, err)
			}
		})
	}
}
