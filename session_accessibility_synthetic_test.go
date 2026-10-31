package perfuncted

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type syntheticAccessibilityAddress struct {
	mu        sync.Mutex
	responses []string
	calls     int
	onCall    func()
}

func (s *syntheticAccessibilityAddress) GetAddress() (string, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.onCall != nil {
		s.onCall()
	}
	if len(s.responses) == 0 {
		return "", nil
	}
	index := s.calls - 1
	if index >= len(s.responses) {
		index = len(s.responses) - 1
	}
	return s.responses[index], nil
}

func startSessionAccessibilityBus(t *testing.T, provider *syntheticAccessibilityAddress) string {
	t.Helper()
	daemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Fatalf("synthetic D-Bus daemon required for this boundary test: %v", err)
	}
	cmd := exec.Command(daemon, "--session", "--nofork", "--nopidfile", "--print-address=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start synthetic D-Bus daemon: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	type addressResult struct {
		address string
		err     error
	}
	addressReady := make(chan addressResult, 1)
	go func() {
		line, readErr := bufio.NewReader(stdout).ReadString('\n')
		addressReady <- addressResult{address: strings.TrimSpace(line), err: readErr}
	}()
	var address string
	select {
	case result := <-addressReady:
		if result.err != nil {
			t.Fatalf("read synthetic D-Bus address: %v", result.err)
		}
		address = result.address
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic D-Bus daemon did not publish its address")
	}

	service, err := dbusutil.SessionBusAddressContext(context.Background(), address)
	if err != nil {
		t.Fatalf("connect synthetic D-Bus service: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(atspiBusService, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim synthetic AT-SPI bus name: reply=%v err=%v", reply, err)
	}
	if err := service.Export(provider, atspiBusPath, atspiBusService); err != nil {
		t.Fatalf("export synthetic AT-SPI bus service: %v", err)
	}
	return address
}

func TestAccessibilityBusAddressRetriesEmptyAddressAndTrimsResult(t *testing.T) {
	provider := &syntheticAccessibilityAddress{responses: []string{"", " \tunix:path=/synthetic/accessibility\n"}}
	busAddress := startSessionAccessibilityBus(t, provider)
	infra := &sessionInfra{dbusAddr: busAddress, timeouts: TimeoutPolicy{Poll: time.Millisecond}}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := infra.accessibilityBusAddress(ctx)
	if err != nil {
		t.Fatalf("resolve synthetic AT-SPI address: %v", err)
	}
	if got != "unix:path=/synthetic/accessibility" {
		t.Fatalf("resolved address %q", got)
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 2 {
		t.Fatalf("GetAddress calls = %d, want one empty response followed by success", calls)
	}
}

func TestAccessibilityBusAddressPropagatesCancellationAndDeadline(t *testing.T) {
	infra := &sessionInfra{dbusAddr: "unix:path=/absent/synthetic-bus", timeouts: TimeoutPolicy{Poll: time.Millisecond}}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := infra.accessibilityBusAddress(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled lookup error = %v", err)
	}

	expired, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()
	if _, err := infra.accessibilityBusAddress(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired lookup error = %v", err)
	}
}

func TestLaunchAccessibilityOwnsAndStopsSyntheticLauncher(t *testing.T) {
	bin := t.TempDir()
	launcher := filepath.Join(bin, atspiBusLauncherName)
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{
		xdgDir:    runtimeDir,
		wlDisplay: "wayland-test",
		dbusAddr:  "unix:path=/synthetic/bus",
		ctx:       context.Background(),
		stopDone:  make(chan struct{}),
	}
	if err := infra.launchAccessibility(); err != nil {
		_ = os.RemoveAll(runtimeDir)
		t.Fatalf("launch synthetic AT-SPI helper: %v", err)
	}
	if infra.atspiCmd == nil {
		t.Fatal("successful launch did not publish process ownership")
	}
	pid, err := readPIDFile(filepath.Join(runtimeDir, "at-spi.pid"))
	if err != nil || pid != infra.atspiCmd.proc.pid {
		t.Fatalf("owned helper pidfile = %d, %v; process pid = %d", pid, err, infra.atspiCmd.proc.pid)
	}
	if err := infra.stop(); err != nil {
		t.Fatalf("stop managed synthetic launcher: %v", err)
	}
	if _, err := os.Stat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("managed runtime remains after stop: %v", err)
	}
}

func TestAccessibilityLauncherSelectionAndCanceledSpawn(t *testing.T) {
	t.Run("distro fallback", func(t *testing.T) {
		fallback := filepath.Join(t.TempDir(), atspiBusLauncherName)
		if err := os.WriteFile(fallback, []byte("synthetic launcher"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", t.TempDir())
		got, err := findAccessibilityLauncher(fallback)
		if err != nil || got != fallback {
			t.Fatalf("fallback selection = %q, %v", got, err)
		}
	})

	t.Run("missing helper", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		got, err := findAccessibilityLauncher(filepath.Join(t.TempDir(), "absent"))
		if err == nil || got != "" {
			t.Fatalf("missing helper selection = %q, %v", got, err)
		}
	})

	t.Run("canceled before spawn", func(t *testing.T) {
		bin := t.TempDir()
		launcher := filepath.Join(bin, atspiBusLauncherName)
		if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		infra := &sessionInfra{ctx: ctx}
		if err := infra.launchAccessibility(); err == nil {
			t.Fatal("launch succeeded with an already-canceled context")
		}
		if infra.atspiCmd != nil {
			t.Fatal("failed spawn published a process handle")
		}
	})
}
