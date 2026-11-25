package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Huge but valid positive startup policies must not wrap the ceiling
// division into a single readiness attempt. That would shorten an explicit
// startup allowance to one poll rather than preserving the caller's policy.
func TestStartupWaitAttemptsDoesNotOverflowCeilingDivision(t *testing.T) {
	for _, tc := range []struct {
		startup time.Duration
		poll    time.Duration
		want    int
	}{
		{time.Duration(math.MaxInt64), 2 * time.Nanosecond, int(math.MaxInt64/2 + 1)},
		{time.Duration(math.MaxInt64), 3 * time.Nanosecond, int(math.MaxInt64/3 + 1)},
		{time.Duration(math.MaxInt64), time.Nanosecond, int(math.MaxInt64)},
	} {
		got := startupWaitAttempts(TimeoutPolicy{Startup: tc.startup, Poll: tc.poll})
		if got != tc.want {
			t.Errorf("startupWaitAttempts(startup=%s, poll=%s)=%d; want %d", tc.startup, tc.poll, got, tc.want)
		}
	}
}

// Unix-domain sockets and managed-session ownership require absolute paths.
// A relative TMPDIR must not publish a relative runtime or misdirect cleanup.
func TestManagedSessionRelativeTMPDIRUsesAbsolutePrivateRuntime(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("private-temp", 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "private-temp")
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatalf("relative TMPDIR startup fallback: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	if !filepath.IsAbs(runtimeDir) {
		t.Fatalf("managed runtime path %q is relative", runtimeDir)
	}
	if !sessionruntime.IsManagedSessionDir(runtimeDir) {
		t.Fatalf("absolute private runtime %q was not recognized for cleanup", runtimeDir)
	}
	if !strings.HasPrefix(runtimeDir, "/tmp/") {
		t.Fatalf("relative TMPDIR did not use absolute safe fallback: %q", runtimeDir)
	}
	for _, socket := range []string{"bus", "wayland-1"} {
		if err := validateManagedSessionSocketPath(filepath.Join(runtimeDir, socket)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteDesktopInputIsRequiredAndNeverFallsBack(t *testing.T) {
	previousPortal := openRemoteDesktopInput
	previousInput := openInput
	t.Cleanup(func() {
		openRemoteDesktopInput = previousPortal
		openInput = previousInput
	})
	portalCalls, fallbackCalls := 0, 0
	openRemoteDesktopInput = func(_ context.Context, _ env.Runtime, options input.RemoteDesktopOptions) (input.Inputter, error) {
		portalCalls++
		if options.ParentWindow != "xdg:test-parent" || !options.PersistPermission {
			t.Fatalf("portal options = %+v", options)
		}
		return nil, input.ErrRemoteDesktopDenied
	}
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) {
		fallbackCalls++
		return nil, errors.New("fallback must not run")
	}
	_, err := Open(context.Background(),
		WithTarget(EnvironmentTarget(nil)),
		WithRemoteDesktopInput(RemoteDesktopInputOptions{ParentWindow: "xdg:test-parent", PersistPermission: true}),
	)
	if !errors.Is(err, input.ErrRemoteDesktopDenied) {
		t.Fatalf("Open error = %v, want portal denial", err)
	}
	if portalCalls != 1 || fallbackCalls != 0 {
		t.Fatalf("portal calls=%d fallback calls=%d, want one portal attempt and no fallback", portalCalls, fallbackCalls)
	}
}

func TestRemoteDesktopInputRejectsOptionalAndDuplicateConfiguration(t *testing.T) {
	newConfig := func() openConfig {
		return openConfig{required: make(map[Capability]struct{}), optional: make(map[Capability]struct{})}
	}
	options := RemoteDesktopInputOptions{}
	cfg := newConfig()
	if err := Optional(CapabilityInput)(&cfg); err != nil {
		t.Fatalf("mark input optional: %v", err)
	}
	if err := WithRemoteDesktopInput(options)(&cfg); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("optional portal input error = %v, want ErrInvalidArgument", err)
	}
	cfg = newConfig()
	if err := WithRemoteDesktopInput(options)(&cfg); err != nil {
		t.Fatalf("configure portal input: %v", err)
	}
	if _, required := cfg.required[CapabilityInput]; !required {
		t.Fatal("portal input did not become required")
	}
	if err := WithRemoteDesktopInput(options)(&cfg); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate portal input error = %v, want ErrInvalidArgument", err)
	}
	if err := Optional(CapabilityInput)(&cfg); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("optional capability after portal input error = %v, want ErrInvalidArgument", err)
	}
}

// Once the private runtime directory exists, an invalid caller-selected log
// root must still fail startup without creating or redirecting any log files.
func TestManagedRuntimePreparationRejectsNonDirectoryLogRoot(t *testing.T) {
	privateRoot := t.TempDir()
	t.Setenv("TMPDIR", privateRoot)
	invalidLogRoot := filepath.Join(t.TempDir(), "not-a-log-directory")
	const original = "unrelated private file"
	if err := os.WriteFile(invalidLogRoot, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	var runtimeDir, logDir string
	err := prepareSessionRuntimeDirs(&runtimeDir, &logDir, invalidLogRoot)
	if err == nil || !strings.Contains(err.Error(), "session: create logs:") {
		t.Fatalf("invalid log root not reported: %v", err)
	}
	if logDir != "" {
		t.Fatalf("failed preparation published log directory %q", logDir)
	}
	if runtimeDir == "" || !sessionruntime.IsPrivateManagedSessionDir(runtimeDir) {
		t.Fatalf("failed preparation omitted owned runtime cleanup handle: %q", runtimeDir)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Errorf("remove owned runtime: %v", err)
		}
	})
	content, readErr := os.ReadFile(invalidLogRoot)
	if readErr != nil || string(content) != original {
		t.Fatalf("failed startup modified unrelated file: content=%q err=%v", content, readErr)
	}
}

// Normal session shutdown must not delete a runtime tree that another account
// can modify or has replaced, even if its path retains the managed prefix.
func TestSessionShutdownRefusesOtherWritableRuntimeReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name   string
		mode   os.FileMode
		remove bool
	}{
		{"private", 0700, true},
		{"group-writable", 0770, false},
		{"world-writable", 0777, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "perfuncted-xdg-"+tc.name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			marker := filepath.Join(dir, "foreign-marker")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			infra := &sessionInfra{xdgDir: dir}
			err := infra.stop()
			_, statErr := os.Stat(marker)
			if tc.remove {
				if err != nil || !os.IsNotExist(statErr) {
					t.Fatalf("private runtime shutdown: err=%v marker=%v", err, statErr)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), "unsafe") || statErr != nil {
					t.Fatalf("untrusted runtime removed or accepted: err=%v marker=%v", err, statErr)
				}
			}
		})
	}
}

// An embedded NUL truncates a Linux sockaddr_un pathname. Preflight must
// reject it rather than approving a path the session cannot actually bind.
func TestManagedSessionSocketPathRejectsEmbeddedNUL(t *testing.T) {
	for _, path := range []string{"/tmp/perfuncted\x00/bus", "\x00", "valid\x00suffix"} {
		if err := validateSessionUnixSocketPath(path); err == nil {
			t.Errorf("accepted nonrepresentable Unix socket pathname %q", path)
		}
		if err := validateManagedSessionSocketPath(path); err == nil {
			t.Errorf("accepted nonrepresentable managed socket pathname %q", path)
		}
	}
	if err := validateManagedSessionSocketPath("/tmp/perfuncted-normal/bus"); err != nil {
		t.Fatalf("ordinary socket pathname rejected: %v", err)
	}
}

func TestManagedSessionDBusRejectsEmbeddedNULBeforeSpawn(t *testing.T) {
	infra := &sessionInfra{dbusAddr: "unix:path=/tmp/perfuncted\x00/bus", ctx: context.Background()}
	err := infra.launchDBus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("D-Bus startup did not reject embedded NUL path: %v", err)
	}
	if infra.dbusCmd != nil {
		t.Fatal("attempted D-Bus process spawn for invalid socket pathname")
	}
}

func TestManagedSessionSocketReadinessRejectsFilesDirectoriesAndSymlinks(t *testing.T) {
	root := shortPrivateSocketDir(t)
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "symlink")
	socket := filepath.Join(root, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		ready      bool
	}{
		{"regular file", regular, false},
		{"directory", directory, false},
		{"symlink to socket", link, false},
		{"real unix socket", socket, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := sessionruntime.WaitForSocket(context.Background(), tc.path, 1, time.Millisecond)
			if tc.ready && err != nil {
				t.Fatalf("real socket rejected: %v", err)
			}
			if !tc.ready && err == nil {
				t.Fatal("non-socket filesystem entry spoofed readiness")
			}
		})
	}
}
