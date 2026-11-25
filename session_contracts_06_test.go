package perfuncted

import (
	"context"
	"errors"
	sessionruntime "github.com/nskaggs/perfuncted/internal/runtime"
	"github.com/nskaggs/perfuncted/window"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Missing runtime directories must not cause discovery to fall back to the
// caller's desktop, nor should an invalid lookalike be accepted as Sway IPC.
func TestSwaySocketRejectsMissingRuntimeAndMalformedSocketNames(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pf-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	missing := &Session{infra: &sessionInfra{xdgDir: filepath.Join(root, "missing")}}
	if found := missing.SwaySocket(); found != "" {
		t.Fatalf("missing owned runtime resolved unrelated socket: %q", found)
	}
	runtime := filepath.Join(root, "owned")
	if err := os.Mkdir(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sway-ipc..sock", "sway-ipc.sock", "sway-ipc.1.socket", "other-ipc.1.sock"} {
		listener, err := net.Listen("unix", filepath.Join(runtime, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
	}
	owned := &Session{infra: &sessionInfra{xdgDir: runtime}}
	if found := owned.SwaySocket(); found != "" {
		t.Fatalf("malformed socket selected: %q", found)
	}
	valid := filepath.Join(runtime, "sway-ipc.123.sock")
	listener, err := net.Listen("unix", valid)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if found := owned.SwaySocket(); found != valid {
		t.Fatalf("valid owned Sway IPC socket not found: %q want %q", found, valid)
	}
}

// A managed session may only offer sockets owned directly by its private
// runtime. A regular file or a symlink to an unrelated desktop's IPC socket
// is not an authorization to connect to that desktop.
func TestSwaySocketSkipsUntrustedLookalikesAndSymlinks(t *testing.T) {
	runtimeDir, err := os.MkdirTemp("/var/tmp", "pf-sway-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreignDir, err := os.MkdirTemp("/var/tmp", "pf-foreign-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(foreignDir) })
	foreign := filepath.Join(foreignDir, "socket")
	foreignListener, err := net.Listen("unix", foreign)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foreignListener.Close() })
	if err := os.Symlink(foreign, filepath.Join(runtimeDir, "sway-ipc.0.sock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "sway-ipc.1.sock"), []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(runtimeDir, "sway-ipc.2.sock")
	listener, err := net.Listen("unix", local)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	session := &Session{infra: &sessionInfra{xdgDir: runtimeDir}}
	if got := session.SwaySocket(); got != local {
		t.Fatalf("chose non-owned or non-socket Sway IPC path: %q instead of %q", got, local)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if got := session.SwaySocket(); got != "" {
		t.Fatalf("untrusted fake Sway IPC socket was published: %q", got)
	}
}

func TestStartSessionReportsUnavailableSwayAndCleansManagedRuntime(t *testing.T) {
	root := prepareHeadlessHelpers(t, true)
	bin := filepath.Join(root, "bin")
	if err := os.Remove(filepath.Join(bin, "sway")); err != nil {
		t.Fatalf("remove synthetic Sway executable: %v", err)
	}

	var events []startupEvent
	pathRestricted := false
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	session.startupObserver = func(phase StartupPhase, state StartupPhaseState) {
		events = append(events, startupEvent{phase: phase, state: state})
		if phase == StartupPhaseDBusSocketReady && state == StartupPhaseSucceeded && !pathRestricted {
			// D-Bus is already running from the synthetic helper. Restrict the
			// subsequent compositor lookup to this directory, which has no Sway.
			t.Setenv("PATH", bin)
			pathRestricted = true
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{
		Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond},
	}, false)
	if infra != nil {
		t.Fatal("failed Sway startup returned live session infrastructure")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("startSession error = %v, want missing Sway executable", err)
	}
	if !pathRestricted {
		t.Fatal("test did not restrict Sway lookup after D-Bus readiness")
	}
	for _, want := range []startupEvent{
		{StartupPhaseSwayProcessSpawn, StartupPhaseStarted},
		{StartupPhaseSwayProcessSpawn, StartupPhaseFailed},
		{StartupPhaseSwayStartup, StartupPhaseFailed},
	} {
		if !hasStartupEvent(events, want) {
			t.Fatalf("failed startup omitted %+v from events %+v", want, events)
		}
	}
	for _, event := range events {
		if event.phase == StartupPhaseSwaySocketsReady {
			t.Fatalf("startup entered socket readiness without a Sway process: %+v", events)
		}
	}
	requireNoManagedSessionDirs(t, root)
}

// A caller-selected short /tmp root must not be duplicated in the cleanup
// search list. This test only inspects roots; it never allocates there.
func TestManagedSessionExplicitShortTempRootIsNotDuplicated(t *testing.T) {
	t.Setenv("TMPDIR", "/tmp")
	roots := sessionruntime.ManagedSessionTempRoots()
	if len(roots) != 1 || roots[0] != "/tmp" {
		t.Fatalf("short root should be unique: %q", roots)
	}
}

// Nested Wayland startup requires an actual parent socket. A missing host
// session must be rejected before opening any log or spawning a compositor.
func TestNestedManagedSessionWithoutHostWaylandRefusesSpawn(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("DISPLAY", "")
	dir := t.TempDir()
	infra := &sessionInfra{ctx: context.Background(), xdgDir: dir, logDir: dir, wlDisplay: "wayland-1"}
	err := infra.launchSway(context.Background(), filepath.Join(dir, "unused.conf"), sessionModeNested)
	if err == nil || !strings.Contains(err.Error(), "requires a host Wayland socket") || infra.swayCmd != nil {
		t.Fatalf("nested startup authorized absent host: err=%v process=%v", err, infra.swayCmd)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "sway-session.log")); !os.IsNotExist(statErr) {
		t.Fatalf("nested startup created a log before host validation: %v", statErr)
	}
}

// An unknown capability must be rejected before a backend opener, session
// registry, or desktop process can be touched.
func TestSessionOpenCapabilityRejectsUnknownIdentifierWithoutBackendMutation(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	unknown := Capability("future-unsupported-backend")
	for _, open := range []struct {
		name string
		call func() (any, error)
	}{
		{name: "contextual", call: func() (any, error) {
			return session.openCapabilityContext(context.Background(), unknown)
		}},
		{name: "legacy", call: func() (any, error) {
			return session.openCapability(unknown)
		}},
	} {
		t.Run(open.name, func(t *testing.T) {
			backend, err := open.call()
			if backend != nil || err == nil || !strings.Contains(err.Error(), "unknown capability") {
				t.Fatalf("unknown capability exposed backend=%v error=%v", backend, err)
			}
		})
	}
	if session.Has(unknown) {
		t.Fatal("unknown backend became available after rejected open")
	}
}

// A failed enumeration cannot be treated as an empty window set. Find and
// Wait must not return a stale handle, and a transient manager read failure
// may be retried only while the caller still authorizes the wait.
func TestWindowDiscoveryFailureDoesNotManufactureWindowOrSuccess(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "stable-1", Title: "private title"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	failure := errors.New("synthetic window discovery connection timeout")
	manager.setError(failure)
	match := WindowMatch{TitleExact: "private title"}
	got, err := session.Windows.Find(context.Background(), match)
	if got != nil || !errors.Is(err, failure) {
		t.Fatalf("failed discovery accepted window=%v err=%v", got, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	got, err = session.Windows.Wait(ctx, match, WaitEvery(time.Millisecond))
	if got != nil || !errors.Is(err, failure) {
		t.Fatalf("unreadable backend yielded window=%v err=%v", got, err)
	}
	manager.setError(nil)
	got, err = session.Windows.Wait(context.Background(), match)
	if err != nil || got == nil || got.ID().String() != "stable-1" {
		t.Fatalf("recovered backend could not discover stable window: window=%v err=%v", got, err)
	}
}

// A failed stable-ID metadata read must not corrupt the cached window handle.
// Once the manager recovers, the same handle can accept a fresh observation.
func TestWindowInfoTransportFailurePreservesCacheAndLaterRecovers(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "stable-id", Title: "original"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := session.Windows.window(window.Info{NativeID: "stable-id", Title: "original"})
	readErr := errors.New("synthetic window transport failure")
	manager.setError(readErr)
	info, err := handle.Info(context.Background())
	if !errors.Is(err, readErr) || info != (window.Info{}) {
		t.Fatalf("failed read leaked info=%+v err=%v", info, err)
	}
	if got := handle.Title(); got != "original" {
		t.Fatalf("failed read changed cached title to %q", got)
	}
	manager.setError(nil)
	manager.setWindows(false, window.Info{NativeID: "stable-id", Title: "updated"})
	info, err = handle.Info(context.Background())
	if err != nil || info.Title != "updated" || handle.Title() != "updated" {
		t.Fatalf("recovered window metadata not accepted: info=%+v err=%v cached=%q", info, err, handle.Title())
	}
}

// A typed nil handle can be passed through the public window APIs. Its
// metadata accessors must return empty values, and operations must refuse
// the absent owner before touching any desktop backend.
func TestWindowAndApplicationUnownedHandleOperationsFailClosed(t *testing.T) {
	var missing *Window
	if got := missing.ID(); got != (WindowID{}) {
		t.Fatalf("nil window exposed identity: %+v", got)
	}
	if title := missing.Title(); title != "" {
		t.Fatalf("nil window exposed title: %q", title)
	}
	if cached := missing.CachedInfo(); cached != (window.Info{}) {
		t.Fatalf("nil window exposed cached info: %+v", cached)
	}
	if _, err := missing.Info(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil window Info returned %v", err)
	}
	if _, err := (&Window{}).Info(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned window Info returned %v", err)
	}
	var windows *WindowBundle
	if got, err := windows.Wait(context.Background(), WindowMatch{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil window bundle wait=%v %v", got, err)
	}
	if got, err := (&WindowBundle{}).Wait(context.Background(), WindowMatch{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned window bundle wait=%v %v", got, err)
	}
	var app *Application
	if got, err := app.WaitForWindow(context.Background(), WindowMatch{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil app wait=%v %v", got, err)
	}
	if got, err := (&Application{}).WaitForWindow(context.Background(), WindowMatch{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned app wait=%v %v", got, err)
	}
}
