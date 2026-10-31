//go:build linux

package window

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

type syntheticShellEval struct {
	mu      sync.Mutex
	scripts []string
	success bool
	result  string
	failure bool
}

func (s *syntheticShellEval) Eval(script string) (bool, string, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts = append(s.scripts, script)
	if s.failure {
		return false, "", dbus.MakeFailedError(errors.New("compositor refused evaluation"))
	}
	return s.success, s.result, nil
}

// startSyntheticGNOMEEvalBus starts an isolated D-Bus daemon and exports a
// deterministic Shell.Eval provider; nothing is sent to the desktop bus.
func startSyntheticGNOMEEvalBus(t *testing.T) (context.Context, string, *syntheticShellEval) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("synthetic D-Bus daemon unavailable")
	}
	address := "unix:path=" + filepath.Join(t.TempDir(), "bus")
	daemon := exec.Command("dbus-daemon", "--session", "--address="+address, "--nofork", "--nopidfile")
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	var server *dbus.Conn
	var err error
	for ctx.Err() == nil {
		server, err = dbusutil.ConnectContext(ctx, address)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("synthetic bus not ready: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if reply, nameErr := server.RequestName(gnomeShellService, dbus.NameFlagDoNotQueue); nameErr != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register private GNOME service: %v %v", reply, nameErr)
	}
	provider := &syntheticShellEval{success: true, result: "ok"}
	if exportErr := server.Export(provider, "/org/gnome/Shell", gnomeShellService); exportErr != nil {
		t.Fatal(exportErr)
	}
	return ctx, address, provider
}

func TestGnomeManagerPrivateDBusEvalSuccessErrorsAndConnectionLifetime(t *testing.T) {
	ctx, address, provider := startSyntheticGNOMEEvalBus(t)
	manager, err := NewGnomeManagerForBusContext(ctx, address)
	if err != nil {
		t.Fatalf("valid GNOME private bus was rejected: %v", err)
	}
	defer manager.Close()
	result, err := manager.eval(ctx, "2 + 2")
	if err != nil || result != "ok" {
		t.Fatalf("successful Shell.Eval result %q %v", result, err)
	}
	provider.mu.Lock()
	if len(provider.scripts) != 2 || provider.scripts[0] != `"ok"` || provider.scripts[1] != "2 + 2" {
		t.Fatalf("wrong shell scripts: %v", provider.scripts)
	}
	provider.success = false
	provider.result = "sandbox evaluation disabled"
	provider.mu.Unlock()
	if _, err := manager.eval(ctx, "forbidden"); err == nil || !strings.Contains(err.Error(), "eval failed") || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Shell refused evaluation: %v", err)
	}
	provider.mu.Lock()
	provider.failure = true
	provider.mu.Unlock()
	if _, err := manager.eval(ctx, "permission-error"); err == nil || !strings.Contains(err.Error(), "compositor refused evaluation") {
		t.Fatalf("D-Bus error lost: %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := manager.eval(canceled, "canceled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled D-Bus eval returned %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.eval(ctx, "after-close"); err == nil {
		t.Fatal("closed GNOME bus still accepted evaluation")
	}
}
