//go:build linux

package window

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/compositor"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/testutil"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
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
	address := "unix:path=" + testutil.ShortSocketPath(t, "bus")
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

const syntheticGnomeWindows = `[{"id":41,"title":"first"},{"id":42,"title":"private-second"}]`

func TestGnomeWindowIteratorRejectsCancelledEvalReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		cancel()
		return syntheticGnomeWindows, nil
	}}
	got, err := mgr.List(ctx)
	if !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatalf("GNOME published windows after a cancelled eval: got=%+v err=%v", got, err)
	}
}

func TestGnomeWindowIteratorStopsPublishingAfterConsumerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		return syntheticGnomeWindows, nil
	}}
	found, cancelled := 0, false
	for info, err := range mgr.IterateWindows(ctx) {
		if errors.Is(err, context.Canceled) {
			if info.ID != 0 {
				t.Fatalf("cancellation leaked window %+v", info)
			}
			cancelled = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		found++
		if found != 1 || info.ID != 41 {
			t.Fatalf("unexpected window after cancellation: %+v", info)
		}
		cancel()
	}
	if found != 1 || !cancelled {
		t.Fatalf("got %d windows, cancellation=%v; want one window then cancellation", found, cancelled)
	}
}

func TestGnomeWindowActionMustNotDispatchAfterPreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) { calls++; return "ok", nil }}
	err := mgr.ActivateByID(ctx, "41")
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("canceled action dispatched=%d error=%v", calls, err)
	}
}

func TestGnomeWindowActionMustNotReportSuccessAfterCancellationDuringEval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) { calls++; cancel(); return "ok", nil }}
	err := mgr.ActivateByID(ctx, "41")
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("in-flight canceled action reported success: dispatched=%d error=%v", calls, err)
	}
}

func TestGnomeFocusedWindowMustNotLeakAfterCancellationDuringEval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) { cancel(); return "private-focus", nil }}
	title, err := mgr.ActiveTitle(ctx)
	if !errors.Is(err, context.Canceled) || title != "" {
		t.Fatalf("canceled focus leaked title=%q err=%v", title, err)
	}
}

func TestGnomeWindowRequestsRejectCancellationBeforeEvaluatingShell(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	mgr := &GnomeManager{evalJS: func(context.Context, string) (string, error) {
		calls++
		return syntheticGnomeWindows, nil
	}}
	if windows, err := mgr.List(ctx); !errors.Is(err, context.Canceled) || len(windows) != 0 {
		t.Fatalf("canceled list result=%+v err=%v", windows, err)
	}
	if title, err := mgr.ActiveTitle(ctx); !errors.Is(err, context.Canceled) || title != "" {
		t.Fatalf("canceled title result=%q err=%v", title, err)
	}
	if calls != 0 {
		t.Fatalf("canceled requests invoked shell %d times", calls)
	}
}

func TestGnomeWindowIteratorDefaultsNilContextAndHonorsValidResult(t *testing.T) {
	calls := 0
	mgr := &GnomeManager{evalJS: func(ctx context.Context, _ string) (string, error) {
		if ctx == nil {
			t.Fatal("nil context forwarded to shell")
		}
		calls++
		return syntheticGnomeWindows, nil
	}}
	// The public API explicitly accepts a missing context and substitutes the
	// default; keep this path covered without passing a nil literal to callers.
	var missingContext context.Context
	windows, err := mgr.List(missingContext)
	if err != nil || len(windows) != 2 || windows[0].ID != 41 || windows[1].ID != 42 {
		t.Fatalf("default context list=%+v err=%v", windows, err)
	}
	if calls != 1 {
		t.Fatalf("expected one shell query, got %d", calls)
	}
}

// A canceled caller must not open a compositor connection even when a real
// bus address is supplied. A missing address must never silently fall back to
// the ambient desktop's default session bus.
func TestGnomeShellEvalConstructorRejectsCanceledOrUnspecifiedBus(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if manager, err := NewGnomeManagerForBusContext(canceled, "unix:path=/not-present/perfuncted-gnome-bus"); manager != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled caller started GNOME bus discovery: manager=%v err=%v", manager, err)
	}
	if manager, err := NewGnomeManagerForBusContext(context.Background(), ""); manager != nil || err == nil || !strings.Contains(err.Error(), "session bus unset") {
		t.Fatalf("missing private bus fell back to ambient session: manager=%v err=%v", manager, err)
	}
}

// The probe must verify a live, usable Shell.Eval capability on an isolated
// service, not infer availability merely from a GNOME desktop label.
func TestGnomeShellEvalProbeRequiresLiveAuthorizedPrivateBus(t *testing.T) {
	_, address, provider := startSyntheticGNOMEEvalBus(t)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	got := checkGnomeShellEval(rt, compositor.GNOME)
	if !got.Available || got.Name != "gnome-shell-eval" || !strings.Contains(got.Reason, "unsafe mode") {
		t.Fatalf("usable private GNOME shell not advertised: %+v", got)
	}
	provider.mu.Lock()
	if len(provider.scripts) != 1 || provider.scripts[0] != `"ok"` {
		t.Errorf("probe did not test exact Shell.Eval authority: %v", provider.scripts)
	}
	provider.failure = true
	provider.mu.Unlock()
	got = checkGnomeShellEval(rt, compositor.GNOME)
	if got.Available || !strings.Contains(got.Reason, "Shell Eval not available") {
		t.Fatalf("revoked Shell.Eval still advertised: %+v", got)
	}
}

// An actual private Shell.Eval service is the fallback authorization for
// GNOME when the native extension is absent; it must not select X11.
func TestGnomeRuntimePrivateShellEvalFallbackWithoutNativeBridge(t *testing.T) {
	_, address, provider := startSyntheticGNOMEEvalBus(t)
	rt := env.FromEnviron([]string{
		"DBUS_SESSION_BUS_ADDRESS=" + address,
		"WAYLAND_DISPLAY=/nonexistent/perfuncted-gnome-probe-socket",
		"XDG_CURRENT_DESKTOP=GNOME",
		"DISPLAY=:999",
	})
	manager, err := openGNOMERuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatalf("usable authorized GNOME Shell.Eval fallback rejected: %v", err)
	}
	defer manager.Close()
	if _, ok := manager.(*GnomeManager); !ok {
		t.Fatalf("unexpected GNOME fallback backend %T", manager)
	}
	provider.mu.Lock()
	calls := len(provider.scripts)
	provider.mu.Unlock()
	if calls == 0 {
		t.Fatal("GNOME fallback never probed Shell.Eval")
	}
}
