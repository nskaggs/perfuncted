//go:build linux

package input

import (
	"context"
	"errors"
	"flag"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A portal grant cannot escape startup unless its persistent token is stored
// atomically; a failed replacement must also close the acquired session.
func TestOpenRemoteDesktopRejectsAndCleansFailedPersistentTokenRotation(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	root := privatePortalTokenFixtureDir(t)
	tokenDir := filepath.Join(root, "state", "perfuncted")
	target := filepath.Join(tokenDir, portalRestoreTokenFile)
	hookResult := make(chan error, 1)
	portal.mu.Lock()
	portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
	portal.persistToken = "new-authorized-grant"
	portal.startHook = func() { hookResult <- os.Mkdir(target, 0700) }
	portal.mu.Unlock()
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
		"XDG_STATE_HOME=" + filepath.Join(root, "state"),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: true})
	if inputter != nil {
		_ = inputter.Close()
		t.Fatalf("failed persistent token rotation returned inputter %T", inputter)
	}
	if err == nil || !strings.Contains(err.Error(), "persist restore token securely") {
		t.Fatalf("token rotation failure = %v, want secure persistence error", err)
	}
	if hookErr := <-hookResult; hookErr != nil {
		t.Fatalf("create directory at token destination: %v", hookErr)
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("failed replacement changed destination: stat=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(tokenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".restore-token-") {
			t.Fatalf("failed replacement left temporary grant %q", entry.Name())
		}
	}
	portal.mu.Lock()
	calls, closes := portal.called, portal.closedSessions
	portal.mu.Unlock()
	if calls != 1 || closes != 1 {
		t.Fatalf("startup failure acquired or retained unexpected portal resources: create calls=%d closes=%d", calls, closes)
	}
}

// Grant rotation must replace the private directory entry, not follow a
// preexisting symlink and overwrite a separate user's data.
func TestPortalTokenRotationReplacesSymlinkWithoutTouchingTarget(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	outer := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(outer, []byte("external credentials"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if err := os.Symlink(outer, path); err != nil {
		t.Fatal(err)
	}
	if err := lock.store("newly-authorized-grant"); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outer); err != nil || string(data) != "external credentials" {
		t.Fatalf("external target changed: %q, %v", data, err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("grant is not private regular file: %v, %v", info, err)
	}
	token, err := lock.consume()
	if err != nil || token != "newly-authorized-grant" {
		t.Fatalf("rotated grant = %q, %v", token, err)
	}
}

// Restore-token state is secret-bearing; its live lock and directory
// descriptors must not be inherited by arbitrary processes started later.
func TestPortalTokenLockDescriptorsCloseOnExec(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for _, fd := range []int{lock.fd, lock.dirfd} {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			t.Fatal(err)
		}
		if flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("sensitive token-store fd %d inherited across exec", fd)
		}
	}
}

const portalTokenChildEnvironment = "PERFUNCTED_TEST_PORTAL_LOCK_CHILD"

func TestPortalTokenLockExcludedAcrossProcessesAndReacquiredAfterRelease(t *testing.T) {
	if os.Getenv(portalTokenChildEnvironment) == "1" {
		rt := env.FromEnviron([]string{"XDG_STATE_HOME=" + os.Getenv("PERFUNCTED_TEST_PORTAL_LOCK_STATE")})
		lock, err := lockPortalTokenStore(rt)
		if lock != nil {
			defer lock.Close()
		}
		switch os.Getenv("PERFUNCTED_TEST_PORTAL_LOCK_EXPECT") {
		case "busy":
			if lock != nil || !errors.Is(err, unix.EWOULDBLOCK) {
				t.Fatalf("foreign process bypassed token-store lock: lock=%v err=%v", lock, err)
			}
		case "free":
			if err != nil || lock == nil {
				t.Fatalf("foreign process could not acquire released lock: lock=%v err=%v", lock, err)
			}
		default:
			t.Fatal("invalid subprocess test role")
		}
		return
	}
	rt := portalTokenTestRuntime(t)
	first, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	runChild := func(role string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPortalTokenLockExcludedAcrossProcessesAndReacquiredAfterRelease$")
		cmd.Env = append(os.Environ(), portalTokenChildEnvironment+"=1", "PERFUNCTED_TEST_PORTAL_LOCK_STATE="+rt.Get("XDG_STATE_HOME"), "PERFUNCTED_TEST_PORTAL_LOCK_EXPECT="+role)
		output, err := cmd.CombinedOutput()
		if ctx.Err() != nil || err != nil {
			t.Fatalf("child role %s: %v (%v): %s", role, err, ctx.Err(), strings.TrimSpace(string(output)))
		}
	}
	runChild("busy")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	runChild("free")
}

// A restore-token store is bound to the directory descriptor it validated.
// If that descriptor becomes unusable, neither a read nor a write may fall
// back to a path-based operation or report successful credential persistence.
func TestPortalTokenStoreInvalidDirectoryDescriptorFailsClosed(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, lockErr := lockPortalTokenStore(rt)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	stateDir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
	if err := lock.store("existing-private-token"); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(stateDir, portalRestoreTokenFile)
	// Invalidate the owned directory descriptor, without changing the path.
	if err := unix.Close(lock.dirfd); err != nil {
		t.Fatal(err)
	}
	lock.dirfd = -1
	if token, err := lock.consume(); token != "" || !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid directory read exposed token=%q err=%v", token, err)
	}
	if err := lock.store("not-persisted"); !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid directory wrote token: %v", err)
	}
	if data, err := os.ReadFile(tokenPath); err != nil || string(data) != "existing-private-token\n" {
		t.Fatalf("invalid descriptor changed durable grant: %q %v", data, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("closing remaining token lock: %v", err)
	}
	reopened, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatalf("invalid prior owner kept store locked: %v", err)
	}
	defer reopened.Close()
	if token, err := reopened.consume(); err != nil || token != "existing-private-token" {
		t.Fatalf("failed operation consumed/changed saved grant: %q %v", token, err)
	}
}

func TestPortalTokenStoreInvalidLockDescriptorRefusesFurtherOperations(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, lockErr := lockPortalTokenStore(rt)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	if err := unix.Close(lock.fd); err != nil {
		t.Fatal(err)
	}
	lock.fd = -1
	if token, err := lock.consume(); token != "" || err == nil {
		t.Fatalf("released lock admitted token read: %q %v", token, err)
	}
	if err := lock.store("unauthorized"); err == nil {
		t.Fatal("released lock admitted token write")
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("closing remaining directory descriptor: %v", err)
	}
	reopened, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatalf("released lock kept resource unavailable: %v", err)
	}
	defer reopened.Close()
	if token, err := reopened.consume(); err != nil || token != "" {
		t.Fatalf("unauthorized store survived invalid owner: %q %v", token, err)
	}
}

func TestPortalTokenStoreHomeFallbackAndMissingRootFailClosed(t *testing.T) {
	home := privatePortalTokenFixtureDir(t)
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

// When the process has no available descriptors, the token-store directory
// must be rejected at its trusted root rather than using ambient paths or
// accepting an unverified restore token. The limit is process-global, so it
// is only changed in an isolated test subprocess.
func TestPortalTokenDirectoryRejectsExhaustedDescriptorTable(t *testing.T) {
	const child = "PF_TEST_PORTAL_TOKEN_DIRECTORY_FD_EXHAUSTED"
	if os.Getenv(child) == "1" {
		root := os.Getenv("PF_TEST_PORTAL_TOKEN_FD_ROOT")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		var original unix.Rlimit
		if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = 0
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limited); err != nil {
			t.Fatal(err)
		}
		fd, openErr := openPrivatePortalDirectory(root)
		restoreErr := unix.Setrlimit(unix.RLIMIT_NOFILE, &original)
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if fd >= 0 {
			_ = unix.Close(fd)
			t.Fatal("descriptor-exhausted token directory unexpectedly opened")
		}
		if !errors.Is(openErr, unix.EMFILE) {
			t.Fatalf("descriptor exhaustion returned %v; want EMFILE", openErr)
		}
		if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
			t.Fatalf("descriptor failure created token-store artifacts: %v, %v", entries, err)
		}
		return
	}
	root := filepath.Join(t.TempDir(), "private-state")
	args := []string{"-test.run=^TestPortalTokenDirectoryRejectsExhaustedDescriptorTable$"}
	if coverage := flag.Lookup("test.gocoverdir"); coverage != nil && coverage.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverage.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), child+"=1", "PF_TEST_PORTAL_TOKEN_FD_ROOT="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated descriptor exhaustion test failed: %v\n%s", err, output)
	}
}

// Replacement must never truncate a foreign hard-linked file. An untrusted
// previous token record can share an inode with unrelated user-owned data.
func TestPortalTokenRotationReplacesHardlinkWithoutModifyingForeignFile(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, lockErr := lockPortalTokenStore(rt)
	if lockErr != nil {
		t.Fatal(lockErr)
	}
	defer lock.Close()

	outside := filepath.Join(t.TempDir(), "unrelated-private-data")
	original := []byte("foreign secret must stay intact\n")
	if err := os.WriteFile(outside, original, 0600); err != nil {
		t.Fatal(err)
	}
	stored := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if err := os.Link(outside, stored); err != nil {
		t.Fatal(err)
	}

	if err := lock.store("fresh-authorized-token"); err != nil {
		t.Fatalf("atomic replacement of untrusted hardlink: %v", err)
	}
	gotForeign, err := os.ReadFile(outside)
	if err != nil || string(gotForeign) != string(original) {
		t.Fatalf("foreign hardlink target modified: got=%q err=%v", gotForeign, err)
	}
	if stat, statErr := os.Stat(stored); statErr != nil || !stat.Mode().IsRegular() || stat.Mode().Perm() != 0600 {
		t.Fatalf("new token not private regular file: stat=%v err=%v", stat, statErr)
	}
	got, err := lock.consume()
	if err != nil || got != "fresh-authorized-token" {
		t.Fatalf("new token not consumed: token=%q err=%v", got, err)
	}
	if _, statErr := os.Lstat(stored); !os.IsNotExist(statErr) {
		t.Fatalf("consumed token still present: %v", statErr)
	}
	gotForeign, err = os.ReadFile(outside)
	if err != nil || string(gotForeign) != string(original) {
		t.Fatalf("foreign file changed after consume: %q %v", gotForeign, err)
	}
}

func TestPortalRestoreTokenRejectsMalformedUTF8FromProviderAndDisk(t *testing.T) {
	malformed := string([]byte{0xff, 0xfe, 0x80})
	runtime := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lock.store(malformed); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("malformed portal token accepted for persistent storage: %v", err)
	}
	tokenPath := filepath.Join(runtime.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("invalid provider token persisted: %v", err)
	}
	if err := os.WriteFile(tokenPath, append([]byte(malformed), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := lock.consume()
	if value != "" || err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("malformed on-disk token consumed: %q %v", value, err)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("malformed token not erased after read: %v", err)
	}
}

// The restore-token trust boundary must reject a descriptor that was closed
// after it was opened, rather than treating an unavailable stat as ownership
// proof or falling back to an untrusted filesystem path.
func TestPortalPrivateFileValidationRejectsClosedDescriptor(t *testing.T) {
	if err := validatePortalPrivateFile(-1); !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid descriptor passed ownership validation: %v", err)
	}
}

// Cleanup is safe for a nil owner, including when startup rejected the token
// store before the lock object was established.
func TestPortalTokenNilOwnerCloseDoesNotTouchDescriptors(t *testing.T) {
	var lock *portalTokenLock
	if err := lock.Close(); err != nil {
		t.Fatalf("nil token lock close: %v", err)
	}
}

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

// The lock file is a security boundary, not just a mutual-exclusion hint.
// Another hard link makes its ownership ambiguous even when both links are
// private regular files; no authorization store may open through that link.
func TestPortalRestoreLockRejectsHardlinkedPrivateLockFile(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	initial, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreLockFile)
	external := filepath.Join(privatePortalTokenFixtureDir(t), "foreign-hardlink")
	if err := os.Link(lockPath, external); err != nil {
		t.Fatal(err)
	}
	lock, err := lockPortalTokenStore(rt)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("hard-linked lock admitted")
	}
	if err == nil {
		t.Fatal("hard-linked lock was not rejected")
	}
	original, readErr := os.ReadFile(external)
	if readErr != nil || len(original) != 0 {
		t.Fatalf("external lock modified: %q %v", original, readErr)
	}
	if err := os.Remove(external); err != nil {
		t.Fatal(err)
	}
	recovered, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatalf("hardlink removal did not restore access: %v", err)
	}
	defer recovered.Close()
	if err := recovered.store("authorized-after-recovery"); err != nil {
		t.Fatal(err)
	}
	if token, err := recovered.consume(); err != nil || token != "authorized-after-recovery" {
		t.Fatalf("recovered store changed grant: %q %v", token, err)
	}
}

// A state path supplied by the environment is never permitted to redirect
// into a relative directory, even if the caller has a valid HOME fallback.
func TestPortalRestoreStateHomeRelativePathFailsClosedWithoutFallback(t *testing.T) {
	root := privatePortalTokenFixtureDir(t)
	rt := env.FromEnviron([]string{"HOME=" + root, "XDG_STATE_HOME=relative/private"})
	lock, err := lockPortalTokenStore(rt)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("relative state root admitted")
	}
	if err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("relative state root did not fail validation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".local", "state", "perfuncted")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid explicit state home silently fell back to HOME: %v", err)
	}
}

// The state hierarchy and the token lock have separate write-permission
// boundaries. Neither may silently redirect persistent portal credentials to
// another root when the existing private directory becomes read-only.
func TestPortalTokenLockRejectsPrivateStoreWriteDenial(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory mode bits")
	}
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, string) string
	}{
		{
			name: "cannot create state directory",
			prepare: func(t *testing.T, root string) string {
				return filepath.Join(root, "state")
			},
		},
		{
			name: "cannot create lock file",
			prepare: func(t *testing.T, root string) string {
				state := filepath.Join(root, "state")
				if err := os.MkdirAll(filepath.Join(state, "perfuncted"), 0700); err != nil {
					t.Fatal(err)
				}
				return state
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := privatePortalTokenFixtureDir(t)
			state := tc.prepare(t, root)
			if err := os.Chmod(root, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.Chmod(root, 0700); err != nil {
					t.Errorf("restore private root: %v", err)
				}
			})
			// The lock-file test must retain traversability to the nested
			// application directory while denying its local creation.
			if tc.name == "cannot create lock file" {
				appDir := filepath.Join(state, "perfuncted")
				if err := os.Chmod(appDir, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(appDir, 0700) })
			}
			lock, err := lockPortalTokenStore(env.FromEnviron([]string{"XDG_STATE_HOME=" + state}))
			if lock != nil || !errors.Is(err, os.ErrPermission) {
				if lock != nil {
					_ = lock.Close()
				}
				t.Fatalf("unwritable private store accepted: lock=%v err=%v", lock, err)
			}
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(state, "perfuncted", portalRestoreLockFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("denied store created or replaced lock file: %v", err)
			}
		})
	}
}

func TestPortalTokenWriteFailurePreservesExistingGrant(t *testing.T) {
	const childEnv = "PF_TEST_PORTAL_TOKEN_FILE_SIZE_LIMIT"
	if os.Getenv(childEnv) == "1" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		secureRoot, err := os.MkdirTemp(home, ".perfuncted-token-test-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(secureRoot)
		state := filepath.Join(secureRoot, "state")
		lock, err := lockPortalTokenStore(env.FromEnviron([]string{"XDG_STATE_HOME=" + state}))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := lock.Close(); err != nil {
				t.Error(err)
			}
		}()
		if err := lock.store("existing-grant"); err != nil {
			t.Fatal(err)
		}
		signal.Ignore(syscall.SIGXFSZ)
		var original syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		limited := original
		limited.Cur = 0
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
			t.Fatal(err)
		}
		storeErr := lock.store("new-grant")
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(storeErr, syscall.EFBIG) {
			t.Fatalf("failed token write returned %v, want EFBIG", storeErr)
		}
		entries, err := os.ReadDir(filepath.Join(state, "perfuncted"))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".restore-token-") {
				t.Fatalf("failed rotation left temporary grant %q", entry.Name())
			}
		}
		token, err := lock.consume()
		if err != nil || token != "existing-grant" {
			t.Fatalf("existing grant after failed rotation = %q, %v", token, err)
		}
		if err := lock.store("recovered-grant"); err != nil {
			t.Fatalf("store did not recover: %v", err)
		}
		return
	}

	dir := t.TempDir()
	args := []string{"-test.run=^TestPortalTokenWriteFailurePreservesExistingGrant$"}
	if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
		args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), childEnv+"=1", "TMPDIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated quota test failed: %v\n%s", err, output)
	}
}

// A private but read-only state parent is not permission to redirect portal
// restore-token storage to an alternate directory. The failing mkdir must not
// leave a lockfile or token behind, even when all ancestors can be traversed.
func TestPortalTokenStoreRejectsReadOnlyStateParentWithoutArtifacts(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0700) })
	rt := env.FromEnviron([]string{"XDG_STATE_HOME=" + parent})
	lock, err := lockPortalTokenStore(rt)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("read-only state parent opened token store")
	}
	if !errors.Is(err, unix.EACCES) {
		t.Fatalf("read-only parent not rejected by mkdir: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(parent, "perfuncted")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rejected parent contains portal artifacts: %v", statErr)
	}
}

// Losing directory ownership between store acquisition and a token operation
// must fail closed without falling back to cwd or any global token path.
func TestPortalTokenRevokedDirectoryDescriptorCannotStoreOrConsume(t *testing.T) {
	lock, err := lockPortalTokenStore(portalTokenTestRuntime(t))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Close(lock.dirfd); err != nil {
		t.Fatal(err)
	}
	lock.dirfd = -1
	if err := lock.store("private-token"); !errors.Is(err, unix.EBADF) {
		t.Fatalf("revoked directory descriptor accepted token write: %v", err)
	}
	if got, err := lock.consume(); got != "" || !errors.Is(err, unix.EBADF) {
		t.Fatalf("revoked directory descriptor exposed token=%q err=%v", got, err)
	}
}

// The filesystem root is not a private application-owned storage directory.
// Opening it directly must fail before a token lock or any credential can be
// created in a world-visible directory.
func TestPortalPrivateDirectoryRejectsFilesystemRoot(t *testing.T) {
	fd, err := openPrivatePortalDirectory("/")
	if fd >= 0 {
		_ = unix.Close(fd)
		t.Fatalf("filesystem root accepted as private token directory: fd=%d", fd)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid component") {
		t.Fatalf("filesystem root rejection=%v", err)
	}
}

// A dangling state path symlink must not be followed or replaced. In
// particular, mkdir-on-miss must not turn a failed O_NOFOLLOW traversal into
// a writable location for a restore credential.
func TestPortalTokenStoreRejectsDanglingStateSymlinkWithoutCreatingGrant(t *testing.T) {
	root := privatePortalTokenFixtureDir(t)
	link := filepath.Join(root, "dangling")
	target := filepath.Join(root, "missing-target")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	rt := env.FromEnviron([]string{"XDG_STATE_HOME=" + filepath.Join(link, "state")})
	lock, err := lockPortalTokenStore(rt)
	if lock != nil {
		_ = lock.Close()
		t.Fatal("dangling state symlink yielded credential store")
	}
	if err == nil {
		t.Fatal("dangling state symlink accepted")
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("foreign symlink was replaced: info=%v err=%v", info, err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("untrusted target materialized: %v", err)
	}
}

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

// A hostile session can replace the grant file with a FIFO. Consumption must
// fail immediately, without waiting for a writer or accepting its data.
func TestPortalTokenConsumeRejectsFIFOWithoutBlockingOrLeakingGrant(t *testing.T) { //nolint:gocyclo // Validate rejection, timing, retained foreign object, and atomic replacement together.
	runtime := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	filename := filepath.Join(runtime.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if mkErr := unix.Mkfifo(filename, 0600); mkErr != nil {
		t.Fatal(mkErr)
	}
	started := time.Now()
	got, consumeErr := lock.consume()
	if got != "" || consumeErr == nil {
		t.Fatalf("FIFO was accepted as a restore credential: token=%q error=%v", got, consumeErr)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO read blocked for %s", elapsed)
	}
	if !strings.Contains(consumeErr.Error(), "restore-token") && !errors.Is(consumeErr, unix.ENXIO) {
		t.Fatalf("unexpected FIFO rejection error: %v", consumeErr)
	}
	fi, err := os.Lstat(filename)
	if err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("failed validation unexpectedly altered foreign FIFO: %v %v", fi, err)
	}
	// Storage must atomically replace, rather than open and write, an untrusted
	// preexisting FIFO when the portal supplies a new authorized grant.
	if storeErr := lock.store("fresh-private-authorization"); storeErr != nil {
		t.Fatalf("atomic replacement of FIFO failed: %v", storeErr)
	}
	fi, err = os.Lstat(filename)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 {
		t.Fatalf("replacement was not a private regular file: %v %v", fi, err)
	}
	if got, err := lock.consume(); err != nil || got != "fresh-private-authorization" {
		t.Fatalf("fresh token not recovered: %q %v", got, err)
	}
}

// Parent directories may be shared only under the root-owned sticky-directory
// exception. An attacker-writable parent must not redirect the restore token.
func TestPortalTokenStatePathRejectsUntrustedStickyNonRootParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "shared-parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 01777); err != nil {
		t.Fatal(err)
	}
	_, err := openPrivatePortalDirectory(filepath.Join(parent, "private", "perfuncted"))
	if err == nil || !strings.Contains(err.Error(), "group or world writable") {
		t.Fatalf("untrusted sticky owner accepted: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(parent, "private")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe parent gained private child: %v", statErr)
	}
}

func TestPortalTokenStatePathAllowsRootOwnedStickyTempParent(t *testing.T) {
	if info, err := os.Stat("/tmp"); err != nil || info.Mode().Perm()&0002 == 0 || info.Mode()&os.ModeSticky == 0 {
		t.Skip("/tmp is not a sticky writable parent")
	}
	path := filepath.Join(privatePortalTokenFixtureDir(t), "state", "perfuncted")
	fd, err := openPrivatePortalDirectory(path)
	if err != nil {
		t.Fatalf("private directory beneath sticky /tmp rejected: %v", err)
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0777 != 0700 || stat.Uid != uint32(unix.Geteuid()) {
		t.Fatalf("wrong private directory ownership/mode: uid=%d mode=%o", stat.Uid, stat.Mode)
	}
}

func TestPortalTokenStatePathRejectsRelativeAndSymlinkComponent(t *testing.T) {
	if fd, err := openPrivatePortalDirectory("relative/state/perfuncted"); err == nil {
		_ = unix.Close(fd)
		t.Fatal("relative private state directory accepted")
	}
	root := t.TempDir()
	real := filepath.Join(root, "actual")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	fd, err := openPrivatePortalDirectory(filepath.Join(alias, "perfuncted"))
	if err == nil {
		_ = unix.Close(fd)
		t.Fatal("symlinked state parent accepted")
	}
	if _, statErr := os.Stat(filepath.Join(real, "perfuncted")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink traversal created private directory: %v", statErr)
	}
}

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

// A grant cannot be returned while its durable record remains replayable.
// Simulate an unlink denial after a valid credential has been read, then
// recover permissions and prove that the same grant is still available once.
func TestPortalRestoreTokenUnlinkFailureDoesNotRevealReplayableGrant(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("unlink-permission denial requires an unprivileged user")
	}
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	const token = "single-use-private-grant"
	if err := lock.store(token); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	got, err := lock.consume()
	if got != "" || (!errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EPERM)) {
		t.Fatalf("unlink-denied grant leaked: value=%q err=%v", got, err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := lock.consume(); got != token || err != nil {
		t.Fatalf("retry after unlink recovery: value=%q err=%v", got, err)
	}
	if got, err := lock.consume(); got != "" || err != nil {
		t.Fatalf("consumed grant replayed: value=%q err=%v", got, err)
	}
}

// A failed permission check during rotation cannot replace or discard the
// previously stored grant, and recovery must not leave a pending new token.
func TestPortalRestoreTokenPermissionDeniedRotationPreservesExistingGrant(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("write-permission denial requires an unprivileged user")
	}
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	const previous = "previous-private-grant"
	if err := lock.store(previous); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Error(err)
		}
	})
	if err := lock.store("unauthorized-replacement"); !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EPERM) {
		t.Fatalf("rotation without directory write permission: %v", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := lock.consume(); got != previous || err != nil {
		t.Fatalf("denied rotation altered original credential: value=%q err=%v", got, err)
	}
	if got, err := lock.consume(); got != "" || err != nil {
		t.Fatalf("denied rotation left a credential behind: value=%q err=%v", got, err)
	}
}

// A state path must not be fabricated through an unwritable parent. In
// particular, failed directory creation must never be treated as successful
// portal restore-token storage, and must not create a partial credential path.
func TestPortalTokenStateDirectoryFailsClosedOnUnwritableParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the private parent write permission")
	}
	root := privatePortalTokenFixtureDir(t)
	parent := filepath.Join(root, "readonly")
	if err := os.Mkdir(parent, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0700) })

	state := filepath.Join(parent, "new-state", "perfuncted")
	fd, err := openPrivatePortalDirectory(state)
	if fd >= 0 {
		_ = unix.Close(fd)
		t.Fatal("unwritable state parent yielded open credential directory")
	}
	if !errors.Is(err, unix.EACCES) && !errors.Is(err, unix.EPERM) {
		t.Fatalf("unwritable parent error = %v, want access denied", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "new-state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unwritable state parent created partial credential directory: %v", err)
	}
}

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
