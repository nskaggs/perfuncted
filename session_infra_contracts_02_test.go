package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagedSwayRejectsStartupWhenEitherRuntimeSocketIsMissing(t *testing.T) {
	for _, tc := range []struct {
		name        string
		readySocket string
		wantError   string
	}{
		{name: "Wayland", readySocket: "ipc", wantError: "wayland socket"},
		{name: "Sway IPC", readySocket: "wayland", wantError: "sway IPC socket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtimeDir, err := createManagedSessionRuntimeDir()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

			readyPath := filepath.Join(runtimeDir, "wayland-1")
			if tc.readySocket == "ipc" {
				readyPath = filepath.Join(runtimeDir, fmt.Sprintf("sway-ipc.%d.1.sock", os.Getuid()))
			}
			readySocket, err := net.Listen("unix", readyPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = readySocket.Close() })

			binDir := t.TempDir()
			fakeSway := filepath.Join(binDir, "sway")
			if err := os.WriteFile(fakeSway, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

			infra := &sessionInfra{
				xdgDir:    runtimeDir,
				wlDisplay: "wayland-1",
				dbusAddr:  "unix:path=/tmp/pf-readiness-bus",
				logDir:    runtimeDir,
				ctx:       context.Background(),
				timeouts:  TimeoutPolicy{Startup: 20 * time.Millisecond, Poll: time.Millisecond},
			}
			err = infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionModeHeadless)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("startup with missing %s socket returned %v", tc.wantError, err)
			}
			if infra.swayCmd == nil {
				t.Fatal("readiness check failed before the compositor process was started")
			}
			waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := infra.swayCmd.Wait(waitCtx, ErrInvalidArgument); err != nil {
				t.Fatalf("fake compositor did not exit cleanly: %v", err)
			}
		})
	}
}

// Managed-session helpers must not consider an arbitrary child of TMPDIR
// a session runtime. Only our managed runtime names are valid write targets.
func TestManagedSessionWritersRejectUnmanagedTempSubdirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	foreign := filepath.Join(root, "unrelated-application-data")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(foreign, "keep")
	if err := os.WriteFile(marker, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: foreign}
	infra.writeChildPID("dbus.pid", os.Getpid())
	if _, err := os.Stat(filepath.Join(foreign, "dbus.pid")); !os.IsNotExist(err) {
		t.Fatalf("managed PID writer wrote into unrelated temp directory: %v", err)
	}
	if config, err := infra.writeEmbeddedConfig("configs/headless.conf", image.Pt(1024, 768)); err == nil || config != "" {
		t.Fatalf("managed config writer accepted unrelated temp directory: %q %v", config, err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "sway.conf")); !os.IsNotExist(err) {
		t.Fatalf("managed config writer modified unrelated temp directory: %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "unrelated" {
		t.Fatalf("foreign marker changed: %q %v", data, err)
	}
}

// Session shutdown never owns an arbitrary caller directory. Even a path
// shaped like a managed runtime must be retained if its ownership or mode is
// inconsistent with a private per-session directory.
func TestManagedSessionShutdownRejectsForeignOrUntrustedRuntimeDirectories(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime func(*testing.T) string
	}{
		{"foreign directory", func(t *testing.T) string { return t.TempDir() }},
		{"nonprivate managed directory", func(t *testing.T) string {
			dir, err := createManagedSessionRuntimeDir()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			if err := os.Chmod(dir, 0777); err != nil {
				t.Fatal(err)
			}
			return dir
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := tc.runtime(t)
			activeRoot := filepath.Join(t.TempDir(), "active-temp-root")
			if err := os.Mkdir(activeRoot, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", activeRoot)
			payload := filepath.Join(dir, "foreign-data")
			if err := os.WriteFile(payload, []byte("preserve me"), 0600); err != nil {
				t.Fatal(err)
			}
			infra := &sessionInfra{xdgDir: dir}
			first := infra.stop()
			second := infra.stop()
			if first == nil || !strings.Contains(first.Error(), "refuse unsafe managed runtime") || second == nil || second.Error() != first.Error() {
				t.Fatalf("unsafe runtime removed or shutdown error unstable: first=%v second=%v", first, second)
			}
			data, err := os.ReadFile(payload)
			if err != nil || string(data) != "preserve me" {
				t.Fatalf("shutdown touched foreign runtime content: data=%q err=%v", data, err)
			}
		})
	}
}

// A failed log setup occurs before sessionInfra is constructed, but the
// runtime was already allocated. The startup transaction must remove that
// directory rather than leak its private socket namespace.
func TestManagedSessionLogCreationFailureRollsBackAllocatedRuntime(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pf-rollback-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("TMPDIR", root)
	blocker := filepath.Join(root, "blocking-file")
	if err := os.WriteFile(blocker, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	session := &Session{}
	infra, startErr := session.startSession(context.Background(), sessionModeHeadless, SessionConfig{LogDir: filepath.Join(blocker, "logs")}, false)
	if infra != nil || startErr == nil || !strings.Contains(startErr.Error(), "create logs") {
		t.Fatalf("invalid log directory admitted: infra=%v err=%v", infra, startErr)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
			t.Fatalf("failed startup leaked managed runtime %q", entry.Name())
		}
	}
	if data, err := os.ReadFile(blocker); err != nil || string(data) != "retain" {
		t.Fatalf("startup altered foreign log blocker: %q %v", data, err)
	}
	if errors.Is(startErr, context.Canceled) {
		t.Fatalf("log filesystem failure misreported as cancellation: %v", startErr)
	}
}
