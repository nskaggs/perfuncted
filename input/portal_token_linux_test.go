//go:build linux

package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestPortalTokenStoreLocksAndConsumesSingleUseToken(t *testing.T) {
	runtime := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(runtime)
	if err != nil {
		t.Fatalf("lockPortalTokenStore: %v", err)
	}
	defer lock.Close()
	if token, consumeErr := lock.consume(); consumeErr != nil || token != "" {
		t.Fatalf("empty consume = %q, %v; want empty token", token, consumeErr)
	}
	if storeErr := lock.store("portal-token"); storeErr != nil {
		t.Fatalf("store token: %v", storeErr)
	}
	second, err := lockPortalTokenStore(runtime)
	if second != nil {
		_ = second.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("second lock error = %v, want locked-store error", err)
	}
	token, consumeErr := lock.consume()
	if consumeErr != nil || token != "portal-token" {
		t.Fatalf("consume = %q, %v; want stored token", token, consumeErr)
	}
	if token, consumeErr := lock.consume(); consumeErr != nil || token != "" {
		t.Fatalf("repeated consume = %q, %v; want empty token", token, consumeErr)
	}
	for _, invalid := range []string{"", "a\nb", "a\rb", "a\x00b", strings.Repeat("x", 4097)} {
		if storeErr := lock.store(invalid); storeErr == nil {
			t.Errorf("store(%q) succeeded, want malformed-token error", invalid)
		}
	}
}

func TestPortalTokenStoreRejectsSymlinkTokenWithoutReadingTarget(t *testing.T) {
	runtime := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(runtime)
	if err != nil {
		t.Fatalf("lockPortalTokenStore: %v", err)
	}
	defer lock.Close()
	privateDir := filepath.Join(runtime.Get("XDG_STATE_HOME"), "perfuncted")
	target := filepath.Join(t.TempDir(), "outside-token")
	if writeErr := os.WriteFile(target, []byte("do-not-read\n"), 0600); writeErr != nil {
		t.Fatalf("write test target: %v", writeErr)
	}
	link := filepath.Join(privateDir, portalRestoreTokenFile)
	if symlinkErr := os.Symlink(target, link); symlinkErr != nil {
		t.Fatalf("create token symlink: %v", symlinkErr)
	}
	if _, consumeErr := lock.consume(); consumeErr == nil {
		t.Fatal("consume followed a symlink token file")
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "do-not-read\n" {
		t.Fatalf("external token target = %q, %v", data, readErr)
	}
}

func TestPortalTokenStoreRejectsSymlinkedStateParent(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatalf("mkdir real state parent: %v", err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink state parent: %v", err)
	}
	runtime := env.FromEnviron([]string{"XDG_STATE_HOME=" + filepath.Join(link, "state")})
	if _, err := lockPortalTokenStore(runtime); err == nil {
		t.Fatal("state traversal followed a symlink parent")
	}
}

func TestPortalTokenStoreRequiresAbsoluteStatePath(t *testing.T) {
	runtime := env.FromEnviron([]string{"XDG_STATE_HOME=relative/state", "HOME="})
	if _, err := lockPortalTokenStore(runtime); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative state path error = %v, want absolute-path validation", err)
	}
}

// privatePortalTokenFixtureDir provides the same ancestor privacy required
// for real persistence. testing.T.TempDir may have group-writable leaf
// permissions under a permissive process umask; relying on it would make
// successful authorization tests fail before exercising portal behavior.
func privatePortalTokenFixtureDir(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp(os.TempDir(), "perfuncted-portal-token-test-")
	if err != nil {
		t.Fatalf("create private temporary directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func portalTokenTestRuntime(t *testing.T) env.Runtime {
	t.Helper()
	return env.FromEnviron([]string{"XDG_STATE_HOME=" + filepath.Join(privatePortalTokenFixtureDir(t), "state")})
}

func TestPrivatePortalTokenFixtureHasSecureAncestorsAndSupportsRealPersistence(t *testing.T) {
	root := privatePortalTokenFixtureDir(t)
	info, err := os.Stat(root)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("fixture root is not private: stat=%v err=%v", info, err)
	}
	lock, err := lockPortalTokenStore(env.FromEnviron([]string{"XDG_STATE_HOME=" + root}))
	if err != nil {
		t.Fatalf("private fixture rejected by real store: %v", err)
	}
	defer lock.Close()
	if err := lock.store("synthetic-authorized-grant"); err != nil {
		t.Fatalf("private token store could not save grant: %v", err)
	}
	if token, err := lock.consume(); err != nil || token != "synthetic-authorized-grant" {
		t.Fatalf("private token store did not recover the grant: token=%q err=%v", token, err)
	}
}
