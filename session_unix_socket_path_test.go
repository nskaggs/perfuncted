package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The live diagnostic used a private TMPDIR that made the D-Bus socket
// pathname longer than Linux's sockaddr_un capacity.
func TestLaunchDBusRejectsTooLongSocketPathBeforeSpawn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("d", 96))
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "bus")
	if len([]byte(socket)) <= 107 {
		t.Fatalf("fixture not long enough: %d", len(socket))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	infra := &sessionInfra{
		xdgDir:   dir,
		dbusAddr: "unix:path=" + socket,
		ctx:      context.Background(),
		timeouts: TimeoutPolicy{Poll: 5 * time.Millisecond, Startup: 130 * time.Millisecond},
	}
	err := infra.launchDBus(ctx)
	if err == nil || !strings.Contains(err.Error(), "socket pathname exceeds") {
		t.Fatalf("want immediate socket-path rejection, got %v", err)
	}
	if infra.dbusCmd != nil {
		t.Fatal("launched D-Bus instead of rejecting path")
	}
}

func TestSessionSocketPathByteBoundary(t *testing.T) {
	path107 := strings.Repeat("x", 107)
	if err := validateSessionUnixSocketPath(path107); err != nil {
		t.Fatalf("107-byte pathname must be permitted: %v", err)
	}
	if err := validateSessionUnixSocketPath(path107 + "x"); !errors.Is(err, ErrSessionSocketPathTooLong) {
		t.Fatalf("108-byte pathname wrong error: %v", err)
	}
	if err := validateSessionUnixSocketPath(strings.Repeat("é", 54)); !errors.Is(err, ErrSessionSocketPathTooLong) {
		t.Fatalf("multibyte path must use byte length: %v", err)
	}
}

// Session runtime paths need more headroom than the Linux sockaddr_un limit:
// dbus-daemon may reject an address that technically fits the socket struct.
func TestManagedSessionConservativeSocketPathBudget(t *testing.T) {
	if err := validateManagedSessionSocketPath(strings.Repeat("a", maxManagedSessionSocketPathBytes)); err != nil {
		t.Fatalf("expected byte boundary to fit: %v", err)
	}
	if err := validateManagedSessionSocketPath(strings.Repeat("a", maxManagedSessionSocketPathBytes+1)); !errors.Is(err, ErrSessionSocketPathTooLong) {
		t.Fatalf("wanted conservative limit before D-Bus startup: %v", err)
	}
	if err := validateManagedSessionSocketPath(strings.Repeat("é", (maxManagedSessionSocketPathBytes/2)+1)); !errors.Is(err, ErrSessionSocketPathTooLong) {
		t.Fatalf("wanted UTF-8 byte-based limit: %v", err)
	}
}

func TestManagedSessionLongTMPDIRAutomaticallyUsesShortPrivateRuntime(t *testing.T) {
	temp := filepath.Join(t.TempDir(), strings.Repeat("x", 70))
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatalf("automatic runtime path shortening failed: %v", err)
	}
	defer os.RemoveAll(runtimeDir)
	if strings.HasPrefix(runtimeDir, temp+string(filepath.Separator)) {
		t.Fatalf("runtime not shortened: %q", runtimeDir)
	}
	for _, socket := range []string{"bus", "wayland-1"} {
		if err := validateManagedSessionSocketPath(filepath.Join(runtimeDir, socket)); err != nil {
			t.Fatalf("runtime still too long: %v", err)
		}
	}
	info, err := os.Stat(runtimeDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("runtime dir should be private 0700: %v %v", info, err)
	}
	if !isManagedSessionDir(runtimeDir) || !isSafeToRemoveDir(runtimeDir) {
		t.Fatal("shortened directory must be recognized for safe cleanup")
	}
	children, err := filepath.Glob(filepath.Join(temp, "perfuncted-xdg-*"))
	if err != nil || len(children) != 0 {
		t.Fatalf("long-path runtime leak: %v %v", children, err)
	}
}

// Reproduces the Sunshine case: the resulting pathname is valid for Linux
// sockaddr_un (<=107 bytes), yet too long for reliable dbus-daemon startup.
func TestManagedSessionPathBelowKernelLimitStillShortensForDBus(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "pf-socket-limit-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	const desiredTMPDIRBytes = 69
	if len(root)+2 > desiredTMPDIRBytes {
		t.Fatalf("test root unexpectedly long: %d", len(root))
	}
	preferred := filepath.Join(root, strings.Repeat("q", desiredTMPDIRBytes-len(root)-1))
	if err := os.MkdirAll(preferred, 0700); err != nil {
		t.Fatal(err)
	}
	// os.MkdirTemp normally generates ten random characters.
	hypotheticalBus := filepath.Join(preferred, "perfuncted-xdg-1234567890", "bus")
	if n := len(hypotheticalBus); n <= maxManagedSessionSocketPathBytes || n > len(syscall.RawSockaddrUnix{}.Path)-1 {
		t.Fatalf("test must be within Linux limit but past D-Bus budget: %d", n)
	}
	t.Setenv("TMPDIR", preferred)
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatalf("failed to fall back below D-Bus budget: %v", err)
	}
	defer os.RemoveAll(runtimeDir)
	if strings.HasPrefix(runtimeDir, preferred+string(os.PathSeparator)) {
		t.Fatalf("failed to shorten a Linux-valid yet D-Bus-risky path: %q", runtimeDir)
	}
	if err := validateManagedSessionSocketPath(filepath.Join(runtimeDir, "bus")); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupStaleSessionsRecognizesShortRuntimeFallback(t *testing.T) {
	temp := filepath.Join(t.TempDir(), strings.Repeat("x", 70))
	if err := os.MkdirAll(temp, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", temp)
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtimeDir)
	if err := os.WriteFile(filepath.Join(runtimeDir, sessionOwnerPIDFile), []byte("99999999"), 0600); err != nil {
		t.Fatal(err)
	}
	cleanupStaleSessionsMu.Lock()
	previous := lastCleanupTime
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()
	t.Cleanup(func() {
		cleanupStaleSessionsMu.Lock()
		lastCleanupTime = previous
		cleanupStaleSessionsMu.Unlock()
	})
	CleanupStaleSessions(24 * time.Hour)
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("shortened abandoned session not reaped: %v", err)
	}
}
