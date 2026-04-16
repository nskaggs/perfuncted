package session

import (
	"context"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestEnviron(t *testing.T) {
	env := Environ("/tmp/test-xdg", "wayland-99", "unix:path=/tmp/test-xdg/bus")

	var xdg, wl, dbus, display, gdk, qt string
	for _, e := range env {
		switch {
		case strings.HasPrefix(e, "XDG_RUNTIME_DIR="):
			xdg = strings.TrimPrefix(e, "XDG_RUNTIME_DIR=")
		case strings.HasPrefix(e, "WAYLAND_DISPLAY="):
			wl = strings.TrimPrefix(e, "WAYLAND_DISPLAY=")
		case strings.HasPrefix(e, "DBUS_SESSION_BUS_ADDRESS="):
			dbus = strings.TrimPrefix(e, "DBUS_SESSION_BUS_ADDRESS=")
		case strings.HasPrefix(e, "DISPLAY="):
			display = strings.TrimPrefix(e, "DISPLAY=")
		case strings.HasPrefix(e, "GDK_BACKEND="):
			gdk = strings.TrimPrefix(e, "GDK_BACKEND=")
		case strings.HasPrefix(e, "QT_QPA_PLATFORM="):
			qt = strings.TrimPrefix(e, "QT_QPA_PLATFORM=")
		}
	}

	if xdg != "/tmp/test-xdg" {
		t.Errorf("XDG_RUNTIME_DIR = %q, want /tmp/test-xdg", xdg)
	}
	if wl != "wayland-99" {
		t.Errorf("WAYLAND_DISPLAY = %q, want wayland-99", wl)
	}
	if dbus != "unix:path=/tmp/test-xdg/bus" {
		t.Errorf("DBUS_SESSION_BUS_ADDRESS = %q", dbus)
	}
	if display != "" {
		t.Errorf("DISPLAY = %q, want empty (cleared)", display)
	}
	if gdk != "wayland" {
		t.Errorf("GDK_BACKEND = %q, want wayland", gdk)
	}
	if qt != "wayland" {
		t.Errorf("QT_QPA_PLATFORM = %q, want wayland", qt)
	}
}

func TestEnvironFiltersHost(t *testing.T) {
	// Set some vars that should be filtered.
	os.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	os.Setenv("WAYLAND_DISPLAY", "wayland-0")
	os.Setenv("DISPLAY", ":0")
	defer os.Unsetenv("DISPLAY")

	env := Environ("/tmp/sess", "wayland-1", "unix:path=/tmp/sess/bus")

	// Count occurrences of XDG_RUNTIME_DIR — should be exactly 1.
	count := 0
	for _, e := range env {
		if strings.HasPrefix(e, "XDG_RUNTIME_DIR=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("XDG_RUNTIME_DIR appears %d times, want 1", count)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	if cfg.Resolution != (image.Point{}) {
		t.Errorf("default Resolution = %v, want zero", cfg.Resolution)
	}
	if cfg.LogDir != "" {
		t.Errorf("default LogDir = %q, want empty", cfg.LogDir)
	}
}

func TestEmbeddedConfigs(t *testing.T) {
	data, err := embeddedConfigs.ReadFile("configs/ci.conf")
	if err != nil {
		t.Fatalf("read embedded ci.conf: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded ci.conf is empty")
	}
	if !strings.Contains(string(data), "HEADLESS-1") {
		t.Error("ci.conf missing HEADLESS-1 output line")
	}

	data, err = embeddedConfigs.ReadFile("configs/headless.conf")
	if err != nil {
		t.Fatalf("read embedded headless.conf: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded headless.conf is empty")
	}
}

func TestStopManagedProcessReapsChild(t *testing.T) {
	s := &Session{}
	cmd := helperCommand(t)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	s.stopManagedProcess(cmd, cmd.Process.Pid, 500*time.Millisecond)
	if err := syscall.Kill(cmd.Process.Pid, 0); err != syscall.ESRCH {
		t.Fatalf("expected process to be gone, got %v", err)
	}
}

func helperCommand(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = env.Merge(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	for {
		time.Sleep(10 * time.Second)
	}
}

func TestCleanupOnSignalStopsOnContextCancel(t *testing.T) {
	xdgDir := filepath.Join(t.TempDir(), "xdg")
	if err := os.MkdirAll(xdgDir, 0700); err != nil {
		t.Fatalf("mkdir xdg: %v", err)
	}
	s := &Session{xdgDir: xdgDir}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	unregister := s.CleanupOnSignal(ctx)
	defer unregister()
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.IsStopped() {
			if _, err := os.Stat(xdgDir); os.IsNotExist(err) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("session was not stopped on context cancellation")
}
