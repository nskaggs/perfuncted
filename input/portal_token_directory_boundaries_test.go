//go:build linux

package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestPortalTokenStoreHomeFallbackAndMissingRootFailClosed(t *testing.T) {
	home := t.TempDir()
	rt := env.FromEnviron([]string{"XDG_STATE_HOME=", "HOME=" + home})
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatalf("HOME fallback rejected: %v", err)
	}
	defer lock.Close()
	path := filepath.Join(home, ".local", "state", "perfuncted")
	stat, err := os.Stat(path)
	if err != nil || !stat.IsDir() || stat.Mode().Perm() != 0700 {
		t.Fatalf("HOME fallback directory %q has stat %v err %v", path, stat, err)
	}
	if storeErr := lock.store("home-fallback-token"); storeErr != nil {
		t.Fatal(storeErr)
	}
	got, err := lock.consume()
	if err != nil || got != "home-fallback-token" {
		t.Fatalf("HOME fallback token %q: %v", got, err)
	}
	if _, err := lockPortalTokenStore(env.FromEnviron([]string{"XDG_STATE_HOME=", "HOME="})); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing safe state root accepted: %v", err)
	}
}

func TestPortalTokenRejectsUnsafeStateDirectoriesAndNonRegularFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
		state string
	}{
		{"world writable parent", func(t *testing.T, root string) {
			t.Helper()
			if err := os.Chmod(root, 0777); err != nil {
				t.Fatal(err)
			}
		}, "state"},
		{"world writable application directory", func(t *testing.T, root string) {
			t.Helper()
			if err := os.MkdirAll(filepath.Join(root, "state", "perfuncted"), 0777); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(root, "state", "perfuncted"), 0777); err != nil {
				t.Fatal(err)
			}
		}, "state"},
		{"non directory state component", func(t *testing.T, root string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(root, "state"), []byte("not a directory"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "state"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(t, root)
			rt := env.FromEnviron([]string{"XDG_STATE_HOME=" + filepath.Join(root, tc.state)})
			lock, err := lockPortalTokenStore(rt)
			if lock != nil {
				_ = lock.Close()
			}
			if err == nil {
				t.Fatalf("accepted %s", tc.name)
			}
		})
	}
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	path := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if token, err := lock.consume(); err == nil || token != "" {
		t.Fatalf("directory masquerading as token accepted: token=%q err=%v", token, err)
	}
}
