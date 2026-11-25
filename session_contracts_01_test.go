package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A working session bus that has not yet published the AT-SPI address must
// preserve the expiration of the caller's wait. An intermediate empty reply
// is useful diagnostic context but must not replace DeadlineExceeded.
func TestAccessibilityAddressTimeoutPreservesDeadlineAfterEmptyReplies(t *testing.T) {
	provider := &syntheticAccessibilityAddress{responses: []string{""}}
	addr := startSessionAccessibilityBus(t, provider)
	infra := &sessionInfra{dbusAddr: addr, timeouts: TimeoutPolicy{Poll: time.Millisecond}}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	address, err := infra.accessibilityBusAddress(ctx)
	if address != "" {
		t.Fatalf("unexpected address %q", address)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("AT-SPI lookup lost deadline after empty successful bus replies: %v", err)
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls == 0 {
		t.Fatal("synthetic AT-SPI provider was never queried")
	}
}

// A compositor opener may ignore its context and finish creating a backend
// after the caller has canceled startup. The session must not install it.
func TestCapabilityOpenRejectsBackendReturnedAfterCancellation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		action       func(context.CancelFunc)
		cancelBefore bool
	}{
		{"canceled before opening", func(context.CancelFunc) {}, true},
		{"canceled while opening", func(cancel context.CancelFunc) { cancel() }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			backend := &trackedCapabilityBackend{}
			opened, configured, installed := 0, 0, 0
			got, err := openCapabilityBackend(ctx, time.Second, CapabilityScreen,
				func(context.Context) (*trackedCapabilityBackend, error) {
					opened++
					tc.action(cancel)
					return backend, nil
				},
				func(*trackedCapabilityBackend) { configured++ },
				func(*trackedCapabilityBackend) { installed++ },
			)
			if !errors.Is(err, context.Canceled) || got != nil {
				t.Fatalf("late backend returned handle=%v err=%v, want nil and canceled", got, err)
			}
			if configured != 0 || installed != 0 {
				t.Fatalf("canceled backend configured=%d installed=%d", configured, installed)
			}
			if opened != 0 && backend.closes != 1 {
				t.Fatalf("late backend closed %d times, want exactly once", backend.closes)
			}
			if tc.cancelBefore && opened != 0 {
				t.Fatalf("pre-canceled startup invoked opener %d times", opened)
			}
		})
	}
}

// Configuration may itself trigger cancellation; installation is still
// forbidden when authority expires between configure and install.
func TestCapabilityOpenRejectsCancellationDuringConfiguration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &trackedCapabilityBackend{}
	installed := 0
	got, err := openCapabilityBackend(ctx, time.Second, CapabilityOutputs,
		func(context.Context) (*trackedCapabilityBackend, error) { return backend, nil },
		func(*trackedCapabilityBackend) { cancel() },
		func(*trackedCapabilityBackend) { installed++ },
	)
	if !errors.Is(err, context.Canceled) || got != nil || installed != 0 || backend.closes != 1 {
		t.Fatalf("canceled configuration handle=%v err=%v installed=%d closes=%d", got, err, installed, backend.closes)
	}
}

// A backend that returns only after the startup deadline has expired must be
// discarded even if its own opener reports success instead of deadline error.
func TestCapabilityOpenRejectsLateSuccessAfterStartupDeadline(t *testing.T) {
	backend := &trackedCapabilityBackend{}
	installed := 0
	got, err := openCapabilityBackend(context.Background(), 3*time.Millisecond, CapabilityClipboard,
		func(ctx context.Context) (*trackedCapabilityBackend, error) {
			<-ctx.Done()
			return backend, nil
		}, nil,
		func(*trackedCapabilityBackend) { installed++ },
	)
	if !errors.Is(err, context.DeadlineExceeded) || got != nil || installed != 0 || backend.closes != 1 {
		t.Fatalf("expired setup handle=%v err=%v installed=%d closes=%d", got, err, installed, backend.closes)
	}
}

// Opening one unavailable capability must not initialize or alter another
// capability. All openers are synthetic: no host desktop is contacted.
func TestCapabilityOpenWindowAndOutputFailuresRemainIsolated(t *testing.T) {
	preserveOpeners(t)
	failure := errors.New("synthetic desktop backend unavailable")
	var windowCalls, outputCalls int
	openWindow = func(context.Context, env.Runtime) (window.Manager, error) {
		windowCalls++
		return nil, failure
	}
	openOutput = func(context.Context, env.Runtime) (output.Lister, error) {
		outputCalls++
		return nil, failure
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	for _, cap := range []Capability{CapabilityWindows, CapabilityOutputs} {
		backend, err := session.openCapabilityContext(context.Background(), cap)
		if backend != nil || !errors.Is(err, failure) {
			t.Fatalf("failed capability %s exposed backend=%v err=%v", cap, backend, err)
		}
	}
	if windowCalls != 1 || outputCalls != 1 {
		t.Fatalf("unexpected opener calls: windows=%d outputs=%d", windowCalls, outputCalls)
	}
	if session.Windows.backend != nil || session.Outputs.backend != nil {
		t.Fatal("failed capability opener mutated unrelated facade backend")
	}
}

// When no managed resolution is configured, keyboard backends must receive
// unconstrained axes rather than inheriting a host display's dimensions.
func TestCapabilityOpenInputWithoutResolutionUsesZeroBounds(t *testing.T) {
	preserveOpeners(t)
	failure := errors.New("synthetic input device unavailable")
	called := false
	openInput = func(_ context.Context, _ env.Runtime, maxX, maxY int32) (input.Inputter, error) {
		called = true
		if maxX != 0 || maxY != 0 {
			t.Errorf("missing managed resolution inherited bounds %dx%d", maxX, maxY)
		}
		return nil, failure
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	backend, err := session.openCapabilityContext(context.Background(), CapabilityInput)
	if !called || backend != nil || !errors.Is(err, failure) {
		t.Fatalf("unconstrained input opener was not rejected: called=%v backend=%v err=%v", called, backend, err)
	}
}

// Successful backend selection binds only the matching facade. In particular,
// opening windows must not implicitly initialize output enumeration or vice versa.
func TestCapabilityOpenWindowAndOutputBindOnlyOwnedFacades(t *testing.T) {
	preserveOpeners(t)
	windows := &listOnlyWindowManager{}
	outputs := &closeCountingOutput{}
	var windowsOpened, outputsOpened int
	openWindow = func(context.Context, env.Runtime) (window.Manager, error) {
		windowsOpened++
		return windows, nil
	}
	openOutput = func(context.Context, env.Runtime) (output.Lister, error) {
		outputsOpened++
		return outputs, nil
	}
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	gotWindow, err := session.openCapabilityContext(context.Background(), CapabilityWindows)
	if err != nil || gotWindow != windows || session.Windows.backend != windows {
		t.Fatalf("window opener did not install exact backend: backend=%v err=%v", gotWindow, err)
	}
	if outputsOpened != 0 || session.Outputs.backend != nil {
		t.Fatal("opening windows unexpectedly initialized the output backend")
	}
	gotOutput, err := session.openCapabilityContext(context.Background(), CapabilityOutputs)
	if err != nil || gotOutput != outputs || session.Outputs.backend != outputs {
		t.Fatalf("output opener did not install exact backend: backend=%v err=%v", gotOutput, err)
	}
	if windowsOpened != 1 || outputsOpened != 1 {
		t.Fatalf("unexpected opener calls windows=%d outputs=%d", windowsOpened, outputsOpened)
	}
}

// An explicit target must not accidentally route desktop requests to the
// host's display, bus, or runtime directory when the target omits them.
func TestResolveRuntimeExplicitTargetNeverInheritsHostDesktop(t *testing.T) {
	t.Setenv("DISPLAY", ":987")
	t.Setenv("WAYLAND_DISPLAY", "host-wayland")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/host/private-bus")
	t.Setenv("XDG_RUNTIME_DIR", "/host/private-runtime")
	target := EnvironmentTarget([]string{"DISPLAY=:46", "DBUS_SESSION_BUS_ADDRESS=unix:path=/explicit/bus", "XDG_RUNTIME_DIR=/explicit/runtime", "TOKEN=private-target"})
	session := &Session{}
	if err := session.resolveRuntime(context.Background(), targetSelection{kind: TargetExplicit, target: target}, false); err != nil {
		t.Fatal(err)
	}
	if session.Target().Kind() != TargetExplicit || session.X11Display() != ":46" || session.XDG() != "/explicit/runtime" || session.DBusAddress() != "unix:path=/explicit/bus" || session.WaylandDisplay() != "" {
		t.Fatalf("explicit desktop routing contaminated: kind=%q x11=%q xdg=%q bus=%q wayland=%q", session.Target().Kind(), session.X11Display(), session.XDG(), session.DBusAddress(), session.WaylandDisplay())
	}
	copied := session.Env()
	for _, item := range copied {
		if strings.Contains(item, "/host/") || strings.Contains(item, "host-wayland") {
			t.Fatalf("host desktop leaked into explicit target: %q", item)
		}
	}
	copied[0] = "TOKEN=overwritten"
	if got := session.env.Get("TOKEN"); got != "private-target" {
		t.Fatalf("caller mutated session routing snapshot: %q", got)
	}
	targetCopy := session.Target().Env()
	if len(targetCopy) == 0 {
		t.Fatal("missing target environment")
	}
	targetCopy[0] = "TOKEN=overwritten"
	if got := session.env.Get("TOKEN"); got != "private-target" {
		t.Fatalf("caller mutated target routing snapshot: %q", got)
	}
}

func TestResolveRuntimeHostTargetUsesCurrentEnvironmentAndRejectsUnknownKind(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/synthetic-host-runtime")
	t.Setenv("DISPLAY", ":99")
	session := &Session{}
	if err := session.resolveRuntime(context.Background(), targetSelection{kind: TargetHost}, false); err != nil {
		t.Fatal(err)
	}
	if session.Target().Kind() != TargetHost || session.XDG() != "/synthetic-host-runtime" || session.X11Display() != ":99" {
		t.Fatalf("host routing mismatch: kind=%q xdg=%q display=%q", session.Target().Kind(), session.XDG(), session.X11Display())
	}
	original := session.Target()
	originalEnv := session.Env()
	err := session.resolveRuntime(context.Background(), targetSelection{kind: TargetKind("other-session")}, false)
	if err == nil || !strings.Contains(err.Error(), "unknown target kind") {
		t.Fatalf("unknown target was accepted: %v", err)
	}
	if session.Target().Kind() != original.Kind() || len(session.Env()) != len(originalEnv) {
		t.Fatalf("unknown target replaced current routing snapshot")
	}
	var nilSession *Session
	if len(nilSession.Env()) != 0 || nilSession.XDG() != "" || nilSession.DBusAddress() != "" || nilSession.WaylandDisplay() != "" || nilSession.X11Display() != "" {
		t.Fatal("nil session exposed environment data")
	}
	if env.FromEnviron(session.Env()).Get("XDG_RUNTIME_DIR") != "/synthetic-host-runtime" {
		t.Fatal("session Env lost host routing")
	}
}

// A final-component Lstat does not protect a cleanup root reached through a
// symlink in an ancestor. The target subtree is unrelated to the chosen root.
func TestCleanupSessionLogsRejectsSymlinkAncestor(t *testing.T) {
	base := t.TempDir()
	outside := filepath.Join(base, "outside")
	logs := filepath.Join(outside, "logs")
	victim := filepath.Join(logs, sessionruntime.SessionLogPrefix+"foreign")
	if err := os.MkdirAll(victim, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "important.txt")
	if err := os.WriteFile(sentinel, []byte("leave alone"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	CleanupSessionLogs(filepath.Join(alias, "logs"), 24*time.Hour)
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "leave alone" {
		t.Fatalf("cleanup followed ancestor symlink: data=%q err=%v", got, err)
	}
}
