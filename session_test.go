package perfuncted

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/clipboard"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"github.com/nskaggs/perfuncted/v2/internal/diagnostic"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	sessionruntime "github.com/nskaggs/perfuncted/v2/internal/runtime"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
)

type closeCountingScreen struct {
	capabilityScreen
	calls atomic.Int32
}

func (s *closeCountingScreen) Close() error {
	s.calls.Add(1)
	return s.capabilityScreen.Close()
}

type closeCountingInput struct {
	calls atomic.Int32
}

func (*closeCountingInput) KeyDown(context.Context, string) error { return nil }
func (*closeCountingInput) KeyUp(context.Context, string) error   { return nil }
func (*closeCountingInput) Type(context.Context, string) error    { return nil }
func (*closeCountingInput) TypeLiteral(context.Context, string) error {
	return nil
}
func (*closeCountingInput) MouseMove(context.Context, int, int) error { return nil }
func (*closeCountingInput) MouseClick(context.Context, int, int, int) error {
	return nil
}
func (*closeCountingInput) MouseDown(context.Context, int) error { return nil }
func (*closeCountingInput) MouseUp(context.Context, int) error   { return nil }
func (*closeCountingInput) ScrollUp(context.Context, int) error  { return nil }
func (*closeCountingInput) ScrollDown(context.Context, int) error {
	return nil
}
func (*closeCountingInput) ScrollLeft(context.Context, int) error { return nil }
func (*closeCountingInput) ScrollRight(context.Context, int) error {
	return nil
}
func (*closeCountingInput) PointerLocation(context.Context) (int, int, error) {
	return 0, 0, input.ErrNotSupported
}
func (*closeCountingInput) Sync(context.Context) error { return nil }
func (s *closeCountingInput) Close() error {
	s.calls.Add(1)
	return nil
}

var (
	_ input.Inputter = (*closeCountingInput)(nil)
	_ window.Manager = (*listOnlyWindowManager)(nil)
	_ output.Lister  = (*closeCountingOutput)(nil)
	_ input.Inputter = (*closeCountingInput)(nil)
)

type closeCountingOutput struct {
	calls atomic.Int32
}

func (o *closeCountingOutput) List(context.Context) ([]output.Info, error) {
	return nil, nil
}
func (o *closeCountingOutput) Close() error {
	o.calls.Add(1)
	return nil
}

func TestSessionCloseUnifiesBundleTeardown(t *testing.T) {
	t.Parallel()
	screenBackend := &closeCountingScreen{}
	inputBackend := &closeCountingInput{}
	windowBackend := &listOnlyWindowManager{}
	outputBackend := &closeCountingOutput{}
	clipboardBackend := &capabilityClipboard{}
	session := newSessionForTesting(screenBackend, inputBackend, windowBackend, outputBackend, clipboardBackend)
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := screenBackend.calls.Load(); got != 1 {
		t.Fatalf("screen close calls = %d, want 1", got)
	}
	if got := inputBackend.calls.Load(); got != 1 {
		t.Fatalf("input close calls = %d, want 1", got)
	}
	if got := outputBackend.calls.Load(); got != 1 {
		t.Fatalf("output close calls = %d, want 1", got)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := screenBackend.calls.Load(); got != 1 {
		t.Fatalf("screen close calls after second Close = %d, want 1", got)
	}
	if got := inputBackend.calls.Load(); got != 1 {
		t.Fatalf("input close calls after second Close = %d, want 1", got)
	}
}

func TestBundleCloseNilSafe(t *testing.T) {
	t.Parallel()
	var screenBundle *screenState
	if err := screenBundle.close(); err != nil {
		t.Fatalf("nil ScreenBundle close = %v, want nil", err)
	}
	var inputBundle *inputState
	if err := inputBundle.Close(); err != nil {
		t.Fatalf("nil InputBundle close = %v, want nil", err)
	}
}

func TestAccessibilityReopenRefreshesCapabilities(t *testing.T) {
	t.Parallel()
	old := &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
	fresh := &bundleAccessibilityFake{gen: 2}
	old.fresh = fresh
	session := newSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	before := session.Capability(CapabilityAccessibility)
	if !before.Available {
		t.Fatal("accessibility capability not available before reopen")
	}
	if err := session.accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	after := session.Capability(CapabilityAccessibility)
	if !after.Available || after.Failure != nil {
		t.Fatalf("capability after reopen = %+v, want available without failure", after)
	}
	wantBackend := "perfuncted.bundleAccessibilityFake"
	_ = wantBackend
	if after.Backend == "" {
		t.Fatal("capability backend empty after reopen")
	}
	if len(after.Operations) == 0 {
		t.Fatal("capability operations empty after reopen")
	}
	apps, err := session.accessibility.Applications(context.Background())
	if err != nil {
		t.Fatalf("Applications after reopen: %v", err)
	}
	_ = apps
	_ = accessibility.ErrDisconnected
}

func TestDiagnosticAtomicWritesLeaveNoPartialFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "manifest.json")
	if err := diagnostic.WriteJSON(jsonPath, map[string]string{"op": "test"}); err != nil {
		t.Fatalf("diagnostic.WriteJSON: %v", err)
	}
	if _, statErr := os.Stat(jsonPath); statErr != nil {
		t.Fatalf("stat manifest: %v", statErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > 12 && name[:12] == ".perfuncted-" {
			t.Fatalf("temp file %q left after diagnostic.WriteJSON", name)
		}
	}
	pngPath := filepath.Join(dir, "screenshot.png")
	if pngErr := diagnostic.WritePNG(pngPath, image.NewRGBA(image.Rect(0, 0, 2, 2))); pngErr != nil {
		t.Fatalf("diagnostic.WritePNG: %v", pngErr)
	}
	entries, readErr = os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > 12 && name[:12] == ".perfuncted-" {
			t.Fatalf("temp file %q left after diagnostic.WritePNG", name)
		}
	}
	failingPath := filepath.Join(dir, "failing.json")
	if err := diagnostic.WriteJSON(failingPath, map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("diagnostic.WriteJSON with unserializable value succeeded, want error")
	}
	if _, statErr := os.Stat(failingPath); !os.IsNotExist(statErr) {
		t.Fatalf("partial file %q exists after failed diagnostic.WriteJSON", failingPath)
	}
}

type focusedATSPISynthetic struct {
	waitAccessibilityBackend
	focused accessibility.Node
	err     error
	calls   int
}

func (b *focusedATSPISynthetic) Focused(ctx context.Context, _ accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.calls++
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	return b.focused, b.err
}

func TestAccessibilityFocusedConditionDistinguishesAbsentTargetErrorAndSuccess(t *testing.T) {
	sourceError := errors.New("synthetic AT-SPI bus read failed")
	for _, tc := range []struct {
		name string
		node accessibility.Node
		err  error
		want bool
	}{
		{"focused node", accessibility.Node{Focused: true, Name: "Search"}, nil, true},
		{"none focused", accessibility.Node{}, accessibility.ErrNotFound, false},
		{"disconnected provider", accessibility.Node{}, sourceError, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &focusedATSPISynthetic{focused: tc.node, err: tc.err}
			session := newSessionForTesting(nil, nil, nil, nil, nil, fake)
			t.Cleanup(func() { _ = session.Close() })
			condition := AccessibilityFocused(accessibility.SnapshotOptions{Fresh: true})
			ok, err := condition.evaluate(context.Background(), session)
			wantErr := tc.err
			if errors.Is(wantErr, accessibility.ErrNotFound) {
				wantErr = nil // absence is a pending state, not an observation failure
			}
			if ok != tc.want || !errors.Is(err, wantErr) {
				t.Fatalf("focus condition: ok=%t err=%v, expected ok=%t err=%v", ok, err, tc.want, wantErr)
			}
			if fake.calls != 1 {
				t.Fatalf("queried focus %d times", fake.calls)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if ok, err := condition.evaluate(canceled, session); ok || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled focused request: ok=%t err=%v", ok, err)
			}
		})
	}
}

func TestAccessibilityFocusedWaitOnlyCompletesOnAuthoritativeFocus(t *testing.T) {
	fake := &focusedATSPISynthetic{err: accessibility.ErrNotFound}
	session := newSessionForTesting(nil, nil, nil, nil, nil, fake)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := session.Wait(ctx, AccessibilityFocused(accessibility.SnapshotOptions{Fresh: true}), WaitEvery(2*time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing focused node must not satisfy wait: %v", err)
	}
	if fake.calls < 2 {
		t.Fatalf("wait did not reread focus: %d queries", fake.calls)
	}
	fake.err = nil
	fake.focused = accessibility.Node{Focused: true}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := session.Wait(ctx2, AccessibilityFocused(accessibility.SnapshotOptions{Fresh: true}), WaitEvery(2*time.Millisecond)); err != nil {
		t.Fatalf("focused node did not satisfy wait: %v", err)
	}
}

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
	t.Setenv("ATSPI_DBUS_IMPLEMENTATION", "dbus-broker")
	bin := t.TempDir()
	launcher := filepath.Join(bin, atspiBusLauncherName)
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nprintf '%s' \"$ATSPI_DBUS_IMPLEMENTATION\" > \"$XDG_RUNTIME_DIR/atspi-implementation\"\nexec /bin/sleep 30\n"), 0700); err != nil {
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
	implementationFile := filepath.Join(runtimeDir, "atspi-implementation")
	if err := sessionruntime.WaitForFile(context.Background(), implementationFile, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if implementation, err := os.ReadFile(implementationFile); err != nil || string(implementation) != "dbus-daemon" {
		t.Errorf("private accessibility bus must use direct daemon activation, not host broker activation: %q %v", implementation, err)
	}
	if infra.atspiCmd == nil {
		t.Fatal("successful launch did not publish process ownership")
	}
	pid, err := sessionruntime.ReadPIDFile(filepath.Join(runtimeDir, "at-spi.pid"))
	if err != nil || pid != infra.atspiCmd.PID() {
		t.Fatalf("owned helper pidfile = %d, %v; process pid = %d", pid, err, infra.atspiCmd.PID())
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

type waitAccessibilityBackend struct {
	calls               int
	incompleteSnapshots int
	alwaysIncomplete    bool
}

func (b *waitAccessibilityBackend) SupportedOperations() []string { return []string{"find", "focused"} }
func (b *waitAccessibilityBackend) Applications(context.Context) ([]accessibility.Application, error) {
	return nil, nil
}
func (b *waitAccessibilityBackend) Snapshot(_ context.Context, root accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	generation := root.Generation
	if generation == 0 {
		generation = 1
	}
	root.Generation = generation
	rootNode := accessibility.Node{ID: root, Role: "application"}
	nodes := []accessibility.Node{rootNode}
	if b.calls >= 2 {
		nodes = append(nodes, accessibility.Node{
			ID:         accessibility.NodeID{BusName: root.BusName, ObjectPath: "/save", Generation: generation},
			Parent:     root,
			Name:       "Save",
			Text:       "Saved",
			Role:       "button",
			States:     []string{"enabled", "focused"},
			Attributes: map[string]string{"kind": "primary"},
		})
	}
	snapshot := accessibility.Snapshot{Root: rootNode, Nodes: nodes, Generation: generation}
	if b.alwaysIncomplete || b.calls <= b.incompleteSnapshots {
		snapshot.Truncated = true
		snapshot.TruncationReasons = []string{"test completeness boundary"}
	}
	return snapshot, nil
}
func (b *waitAccessibilityBackend) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	snapshot, err := b.Snapshot(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	return accessibility.FilterSnapshot(snapshot, query), nil
}
func (b *waitAccessibilityBackend) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{Focused: true}, nil
}
func (b *waitAccessibilityBackend) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{}, accessibility.ErrNotFound
}
func (b *waitAccessibilityBackend) Close() error { return nil }

func TestAccessibilityWaitConditionsRefreshUntilSatisfied(t *testing.T) {
	backend := &waitAccessibilityBackend{}
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/root"}
	if err := session.Wait(ctx, AccessibilityNodeExists(root, accessibility.Query{Role: "button"}, accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityNodeExists: %v", err)
	}
	if err := session.Wait(ctx, AccessibilityStateContains(root, accessibility.Query{Name: "Save"}, []string{"focused", "enabled"}, accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityStateContains: %v", err)
	}
	if err := session.Wait(ctx, AccessibilityTextContains(root, "Saved", accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityTextContains: %v", err)
	}
	if backend.calls < 3 {
		t.Fatalf("backend calls = %d, want repeated authoritative refreshes", backend.calls)
	}
	_ = session.Close()
}

func TestAccessibilityNodeExistsWaitRetriesIncompleteSnapshots(t *testing.T) {
	backend := &waitAccessibilityBackend{incompleteSnapshots: 1}
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	condition := AccessibilityNodeExists(
		accessibility.NodeID{BusName: "org.test", ObjectPath: "/root"},
		accessibility.Query{Role: "button"},
		accessibility.SnapshotOptions{},
	)
	var failures waitEvaluateFailures
	if ok, err := session.evaluateWaitCondition(context.Background(), condition, &failures); ok || err != nil || failures.consecutive != 1 || failures.total != 1 {
		t.Fatalf("first incomplete evaluation = ok %t, err %v, consecutive %d, total %d; want retryable false", ok, err, failures.consecutive, failures.total)
	}
	if ok, err := session.evaluateWaitCondition(context.Background(), condition, &failures); !ok || err != nil || failures.consecutive != 0 || failures.total != 1 {
		t.Fatalf("complete evaluation = ok %t, err %v, consecutive %d, total %d; want success", ok, err, failures.consecutive, failures.total)
	}
	if backend.calls != 2 {
		t.Fatalf("snapshot calls = %d, want incomplete then complete", backend.calls)
	}
}

func TestAccessibilityNodeExistsWaitSurfacesSustainedIncompleteSnapshots(t *testing.T) {
	backend := &waitAccessibilityBackend{alwaysIncomplete: true}
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	condition := AccessibilityNodeExists(
		accessibility.NodeID{BusName: "org.test", ObjectPath: "/root"},
		accessibility.Query{Role: "button"},
		accessibility.SnapshotOptions{},
	)
	var failures waitEvaluateFailures
	for i := 1; i < waitEvaluateFailureLimit; i++ {
		if ok, err := session.evaluateWaitCondition(context.Background(), condition, &failures); ok || err != nil {
			t.Fatalf("incomplete evaluation %d = ok %t, err %v; want retryable false", i, ok, err)
		}
	}
	if ok, err := session.evaluateWaitCondition(context.Background(), condition, &failures); ok || !errors.Is(err, accessibility.ErrIncompleteSnapshot) {
		t.Fatalf("terminal incomplete evaluation = ok %t, err %v; want ErrIncompleteSnapshot", ok, err)
	}
	if backend.calls != waitEvaluateFailureLimit {
		t.Fatalf("snapshot calls = %d, want bounded failure limit %d", backend.calls, waitEvaluateFailureLimit)
	}
}

type waitAccessibilityEventBackend struct {
	waitAccessibilityBackend
	events chan accessibility.Event

	// opened closes once the wait machinery has opened an event stream, and
	// delivered closes once an event has been handed to that stream. A test
	// needs both to claim an event arrived during a wait, rather than before
	// the wait was subscribed.
	opened        chan struct{}
	delivered     chan struct{}
	openedOnce    sync.Once
	deliveredOnce sync.Once
}

func newWaitAccessibilityEventBackend() *waitAccessibilityEventBackend {
	return &waitAccessibilityEventBackend{
		events:    make(chan accessibility.Event, 1),
		opened:    make(chan struct{}),
		delivered: make(chan struct{}),
	}
}

func (b *waitAccessibilityEventBackend) Events(
	ctx context.Context,
	_ accessibility.EventOptions,
) (<-chan accessibility.Event, error) {
	b.openedOnce.Do(func() { close(b.opened) })
	out := make(chan accessibility.Event, 8)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-b.events:
				if !ok {
					return
				}
				select {
				case out <- event:
					b.deliveredOnce.Do(func() { close(b.delivered) })
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func TestWaitChangesIncludesAccessibilityEvents(t *testing.T) {
	backend := newWaitAccessibilityEventBackend()
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	wake := session.waitChanges()
	backend.events <- accessibility.Event{Kind: "focus"}
	select {
	case <-wake.done:
	case <-time.After(time.Second):
		t.Fatal("accessibility event did not wake waiter")
	}
}

func TestWaitEpochKeepsWakeAttributionAcrossRapidNotifications(t *testing.T) {
	hub := newInvalidationHub()
	epoch := hub.subscribe()
	observed := make(chan waitChange, 1)
	go func() {
		<-epoch.done
		observed <- epoch.change
	}()
	hub.notify(waitChange{source: "window"})
	hub.notify(waitChange{source: "accessibility", event: &accessibility.Event{Kind: "state-changed", Property: "enabled"}})
	select {
	case change := <-observed:
		if change.source != "window" || change.event != nil {
			t.Fatalf("first epoch attribution = %+v, want window without event", change)
		}
	case <-time.After(time.Second):
		t.Fatal("epoch waiter did not wake")
	}
}

func TestWaitWithEvidenceReportsAccessibilityWake(t *testing.T) {
	backend := newWaitAccessibilityEventBackend()
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()

	// The wait opens its event stream lazily, on its first evaluation, so the
	// event is sent once the stream exists. The condition then blocks until the
	// event has entered the stream and only reports satisfied on the evaluation
	// after the wake it produced, so the wait ends on the wakeup rather than on
	// a timeout. The poll interval is long enough that the event, not the poll,
	// is what wakes the wait, which is what the evidence asserts.
	go func() {
		<-backend.opened
		backend.events <- accessibility.Event{Kind: "state-changed", Property: "enabled"}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	evaluations := 0
	evidence, err := session.WaitWithEvidence(ctx, sessionCondition("after event", func(ctx context.Context, _ *sessionCore) (bool, error) {
		evaluations++
		if evaluations == 1 {
			select {
			case <-backend.delivered:
			case <-ctx.Done():
				return false, ctx.Err()
			}
			return false, nil
		}
		return true, nil
	}), WaitEvery(250*time.Millisecond))
	if err != nil {
		t.Fatalf("WaitWithEvidence: %v", err)
	}
	if evidence.AccessibilityWakeups == 0 || evidence.LastAccessibilityEvent == nil {
		t.Fatalf("wait evidence = %+v, want accessibility wakeup and event", evidence)
	}
	if evidence.LastAccessibilityEvent.Kind != "state-changed" {
		t.Fatalf("last accessibility event = %+v", evidence.LastAccessibilityEvent)
	}
	if evidence.PollWakeups != 0 {
		t.Fatalf("wait evidence = %+v, want the wake attributed to the event rather than a poll", evidence)
	}
}

func TestStopSkipsNonSafeDir(t *testing.T) {
	dir := t.TempDir()
	infra := &sessionInfra{xdgDir: "/"}
	if err := infra.stop(); err == nil {
		t.Fatal("unsafe runtime cleanup unexpectedly succeeded")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("tmp dir stat: %v", err)
	}
	infra2Dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	infra2 := &sessionInfra{xdgDir: infra2Dir}
	sentinel := filepath.Join(infra2.xdgDir, "keep")
	if err := os.WriteFile(sentinel, []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := infra2.stop(); err != nil {
		t.Fatalf("managed runtime cleanup: %v", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("expected managed dir to be removed, sentinel stat = %v", err)
	}
	if _, err := os.Stat(infra2Dir); !os.IsNotExist(err) {
		t.Fatalf("expected managed dir to be removed, dir stat = %v", err)
	}
}

func TestManagedSwayConfigSelectionStaysInsideRuntime(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	infra := &sessionInfra{xdgDir: runtimeDir}

	headless, err := infra.resolveSwayConfig(sessionModeHeadless, image.Pt(1600, 900))
	if err != nil {
		t.Fatalf("resolve headless config: %v", err)
	}
	if filepath.Dir(headless) != runtimeDir {
		t.Fatalf("headless config escaped managed runtime: %q", headless)
	}
	data, err := os.ReadFile(headless)
	if err != nil || !strings.Contains(string(data), "resolution 1600x900") {
		t.Fatalf("headless resolution was not applied: %v", err)
	}

	nested, err := infra.resolveSwayConfig(sessionModeNested, image.Pt(1600, 900))
	if err != nil {
		t.Fatalf("resolve nested config: %v", err)
	}
	data, err = os.ReadFile(nested)
	wantNested, embeddedErr := embeddedConfigs.ReadFile("configs/nested.conf")
	if err != nil || embeddedErr != nil || string(data) != string(wantNested) {
		t.Fatalf("nested config differs from its embedded layout: read=%v embedded=%v", err, embeddedErr)
	}
	if _, err := infra.resolveSwayConfig(sessionMode(99), image.Point{}); err == nil {
		t.Fatal("unknown session mode selected a Sway config")
	}

	unsafeDir := t.TempDir()
	activeTempRoot := filepath.Join(t.TempDir(), "active")
	if err := os.Mkdir(activeTempRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", activeTempRoot)
	if sessionruntime.IsSafeToRemoveDir(unsafeDir) {
		t.Fatalf("fixture path %q unexpectedly qualifies as safe", unsafeDir)
	}
	if _, err := (&sessionInfra{xdgDir: unsafeDir}).writeEmbeddedConfig("configs/headless.conf", image.Point{}); err == nil {
		t.Fatal("configuration writer accepted a directory outside managed runtimes")
	}
}

func TestWriteChildPIDRequiresSafeLocationAndPositivePID(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	infra := &sessionInfra{xdgDir: runtimeDir}
	infra.writeChildPID("child.pid", 0)
	if _, err := os.Stat(filepath.Join(runtimeDir, "child.pid")); !os.IsNotExist(err) {
		t.Fatalf("nonpositive child pid created a file: %v", err)
	}
	infra.writeChildPID("child.pid", os.Getpid())
	pid, err := sessionruntime.ReadPIDFile(filepath.Join(runtimeDir, "child.pid"))
	if err != nil || pid != os.Getpid() {
		t.Fatalf("owned runtime pidfile = %d, %v", pid, err)
	}

	unsafeDir := t.TempDir()
	activeTempRoot := filepath.Join(t.TempDir(), "active")
	if err := os.Mkdir(activeTempRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", activeTempRoot)
	if sessionruntime.IsSafeToRemoveDir(unsafeDir) {
		t.Fatalf("fixture path %q unexpectedly qualifies as safe", unsafeDir)
	}
	(&sessionInfra{xdgDir: unsafeDir}).writeChildPID("child.pid", os.Getpid())
	if _, err := os.Stat(filepath.Join(unsafeDir, "child.pid")); !os.IsNotExist(err) {
		t.Fatalf("pidfile was written outside an owned runtime: %v", err)
	}
	var absent *sessionInfra
	absent.writeChildPID("child.pid", os.Getpid())
}

func TestManagedLaunchersRefuseUnavailableStartupInputs(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	logDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("WAYLAND_DISPLAY", "")
	noHelpers := t.TempDir()
	t.Setenv("PATH", noHelpers)

	infra := &sessionInfra{
		xdgDir:    runtimeDir,
		wlDisplay: "wayland-1",
		dbusAddr:  "unix:path=" + filepath.Join(runtimeDir, "bus"),
		logDir:    logDir,
		ctx:       context.Background(),
	}
	if err := infra.launchDBus(context.Background()); err == nil {
		t.Fatal("D-Bus launcher unexpectedly started without its executable")
	}
	if infra.dbusCmd != nil {
		t.Fatal("failed D-Bus launch published a process handle")
	}

	if err := infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionMode(99)); err == nil {
		t.Fatal("Sway launcher accepted an unknown session mode")
	}
	if infra.swayCmd != nil {
		t.Fatal("rejected Sway mode published a process handle")
	}
	if err := infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionModeNested); err == nil || !strings.Contains(err.Error(), "host Wayland socket") {
		t.Fatalf("nested Sway launch without a host socket returned %v", err)
	}
	if infra.swayCmd != nil {
		t.Fatal("rejected nested launch published a process handle")
	}

	if err := infra.launchWlPaste(); err == nil {
		t.Fatal("optional clipboard helper unexpectedly started without its executable")
	}
	if infra.wlPasteCmd != nil {
		t.Fatal("failed clipboard helper start published a process handle")
	}
}

// A session's runtime can already have been removed by independent stale
// cleanup. Stop must join successfully instead of interpreting the missing
// path as an unsafe replacement or trying to remove neighboring files.
func TestManagedSessionStopToleratesAlreadyRemovedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", testutil.ShortRuntimeDir(t))
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	neighbor := filepath.Join(root, "unrelated")
	if err := os.WriteFile(neighbor, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(runtimeDir); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	if err := infra.stop(); err != nil {
		t.Fatalf("already removed runtime stop: %v", err)
	}
	if err := infra.stop(); err != nil {
		t.Fatalf("idempotent stop: %v", err)
	}
	data, err := os.ReadFile(neighbor)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("neighbor modified during absent-runtime teardown: %q err=%v", data, err)
	}
	if _, err := os.Lstat(runtimeDir); !os.IsNotExist(err) {
		t.Fatalf("runtime resurrected by stop: %v", err)
	}
}

// There is no handler to register for an uninitialized infrastructure. The
// unregister callback must still be safe to invoke repeatedly.
func TestNilManagedInfraSignalCleanupReturnsHarmlessUnregister(t *testing.T) {
	var infra *sessionInfra
	unregister := infra.CleanupOnSignal(context.Background())
	if unregister == nil {
		t.Fatal("nil infrastructure returned no unregister callback")
	}
	unregister()
	unregister()
}

func TestSessionSwayConfigRejectsCancellationAfterPhaseSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var states []StartupPhaseState
	session := &sessionCore{startupObserver: func(phase StartupPhase, state StartupPhaseState) {
		if phase != StartupPhaseSwayConfiguration {
			return
		}
		states = append(states, state)
		if state == StartupPhaseSucceeded {
			cancel()
		}
	}}

	path, err := session.resolveSessionSwayConfig(ctx, &sessionInfra{}, sessionModeNested, SessionConfig{SwayConfigPath: "/private/sway.conf"})
	if !errors.Is(err, context.Canceled) || path != "" {
		t.Fatalf("late-canceled Sway config = %q, %v; want no published path and caller cancellation", path, err)
	}
	if len(states) != 2 || states[0] != StartupPhaseStarted || states[1] != StartupPhaseSucceeded {
		t.Fatalf("Sway configuration phase states = %v, want started then succeeded before late cancellation", states)
	}
}

func TestSessionSignalCleanupReportsUnsafeRuntimeFailureAfterCancellation(t *testing.T) {
	runtimeDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir, stopDone: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	unregister := infra.CleanupOnSignal(ctx)
	cancel()
	defer unregister()

	select {
	case <-infra.stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("context cancellation did not complete session cleanup")
	}
	if infra.stopErr == nil {
		t.Fatal("cleanup failure for an unmanaged runtime was not retained")
	}
	if _, err := os.Stat(runtimeDir); err != nil {
		t.Fatalf("unsafe runtime should be preserved after failed cleanup: %v", err)
	}
}

func TestManagedSwayLogCreationFailureStopsBeforeProcessSpawn(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	logBlocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(logBlocker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{
		xdgDir:    runtimeDir,
		wlDisplay: "wayland-1",
		logDir:    logBlocker,
		ctx:       context.Background(),
	}
	err = infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionModeHeadless)
	if err == nil || !strings.Contains(err.Error(), "create log") {
		t.Fatalf("startup with an unusable log directory returned %v", err)
	}
	if infra.swayCmd != nil {
		t.Fatal("Sway was started before its private log could be created")
	}
	if contents, readErr := os.ReadFile(logBlocker); readErr != nil || string(contents) != "preserve" {
		t.Fatalf("log setup failure changed its blocking file: %q %v", contents, readErr)
	}
}

func TestManagedSwayConfigPublishErrorPreservesObstructingDirectory(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	target := filepath.Join(runtimeDir, "sway.conf")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "preserve")
	if err := os.WriteFile(sentinel, []byte("existing state"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath, err := (&sessionInfra{xdgDir: runtimeDir}).writeEmbeddedConfig("configs/headless.conf", image.Point{})
	if err == nil || configPath != "" || !strings.Contains(err.Error(), "write config") {
		t.Fatalf("config publication over a directory returned path=%q err=%v", configPath, err)
	}
	if contents, readErr := os.ReadFile(sentinel); readErr != nil || string(contents) != "existing state" {
		t.Fatalf("failed config publication changed obstructing state: %q %v", contents, readErr)
	}
	entries, readErr := os.ReadDir(runtimeDir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "sway.conf" || !entries[0].IsDir() {
		t.Fatalf("failed config publication leaked staged data or replaced the directory: %v %v", entries, readErr)
	}
}

// An invalid caller-selected temporary root must stop managed startup before
// any log allocation. The setup wrapper must preserve the underlying error
// and leave both output paths uninitialized, not fall back to an unrelated root.
func TestManagedRuntimePreparationRejectsMissingPreferredTempRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-preferred-temporary-root")
	t.Setenv("TMPDIR", missing)
	logs := filepath.Join(t.TempDir(), "uncreated-private-logs")
	var runtimeDir, logDir string
	err := prepareSessionRuntimeDirs(&runtimeDir, &logDir, logs)
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "managed runtime") {
		t.Fatalf("missing caller temp root: %v", err)
	}
	if runtimeDir != "" || logDir != "" {
		t.Fatalf("failed startup published runtime=%q logs=%q", runtimeDir, logDir)
	}
	if _, statErr := os.Lstat(logs); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed startup allocated log root: %v", statErr)
	}
}

// A matching managed-runtime name is insufficient authority to create PID
// metadata or compositor config in a directory another account can modify.
func TestManagedSessionWritersRejectOtherWritableRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name  string
		mode  os.FileMode
		allow bool
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
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			infra := &sessionInfra{xdgDir: dir}
			infra.writeChildPID("dbus.pid", os.Getpid())
			_, pidErr := os.Lstat(filepath.Join(dir, "dbus.pid"))
			_, configErr := infra.writeEmbeddedConfig("configs/headless.conf", image.Pt(1280, 720))
			_, confStatErr := os.Lstat(filepath.Join(dir, "sway.conf"))
			if tc.allow {
				if pidErr != nil || configErr != nil || confStatErr != nil {
					t.Fatalf("private runtime rejected: pid=%v config=%v stat=%v", pidErr, configErr, confStatErr)
				}
			} else {
				if !os.IsNotExist(pidErr) || configErr == nil || !os.IsNotExist(confStatErr) {
					t.Fatalf("writable runtime admitted: pid=%v config=%v stat=%v", pidErr, configErr, confStatErr)
				}
			}
		})
	}
}

// Invalid caller log storage must not leak the freshly allocated managed
// desktop runtime, and an already canceled operation must allocate nothing.
func TestManagedSessionPreparationAndCancellationFailBeforeSpawning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session := &sessionCore{}
	if infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{}, false); infra != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup: infra=%v err=%v", infra, err)
	}
	root := t.TempDir()
	blocker := filepath.Join(root, "foreign-file")
	if err := os.WriteFile(blocker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	var runtimeDir, logDir string
	err := prepareSessionRuntimeDirs(&runtimeDir, &logDir, filepath.Join(blocker, "logs"))
	if err == nil || runtimeDir == "" || logDir != "" {
		t.Fatalf("unsafe log root accepted: runtime=%q logs=%q err=%v", runtimeDir, logDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	if data, readErr := os.ReadFile(blocker); readErr != nil || string(data) != "preserve" {
		t.Fatalf("foreign file altered: %q %v", data, readErr)
	}
	if err := os.RemoveAll(runtimeDir); err != nil {
		t.Fatal(err)
	}
}

func TestManagedSessionInvalidConfigurationAndDaemonAddressNeverSpawn(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	infra := &sessionInfra{xdgDir: runtimeDir, dbusAddr: "tcp:host=example.org", ctx: context.Background()}
	if err := infra.launchDBus(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid D-Bus socket address") || infra.dbusCmd != nil {
		t.Fatalf("invalid daemon address admitted: %v", err)
	}
	if _, err := infra.writeEmbeddedConfig("configs/not-a-configuration.conf", image.Point{}); err == nil || !strings.Contains(err.Error(), "read embedded config") {
		t.Fatalf("missing embedded config accepted: %v", err)
	}
	if _, err := (&sessionCore{}).resolveSessionSwayConfig(context.Background(), infra, sessionMode(-8), SessionConfig{}); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("invalid compositor mode accepted: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(runtimeDir, "sway.conf")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid config was written: %v", statErr)
	}
}

func TestManagedRuntimeCreationRefusesUnusableTemporaryRoot(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing-root"))
	if dir, err := createManagedSessionRuntimeDir(); err == nil || dir != "" {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		t.Fatalf("missing caller temp root accepted: dir=%q err=%v", dir, err)
	}
	t.Setenv("TMPDIR", "relative-root")
	roots := sessionruntime.ManagedSessionTempRoots()
	if len(roots) != 1 || roots[0] != "/tmp" {
		t.Fatalf("relative socket root accepted: %+v", roots)
	}
}

func TestManagedChildPIDWriteFailurePreservesExistingDirectory(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	target := filepath.Join(runtimeDir, "child.pid")
	if mkdirErr := os.Mkdir(target, 0700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	(&sessionInfra{xdgDir: runtimeDir}).writeChildPID("child.pid", os.Getpid())
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("pid writer replaced foreign directory: %+v %v", info, err)
	}
}

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
	session := &sessionCore{}
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

// Managed runtime names are not authority to overwrite arbitrary files:
// a preexisting symlink must never redirect generated Sway config writes.
func TestManagedSwayConfigWriteCannotClobberSymlinkTarget(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreign := filepath.Join(t.TempDir(), "foreign-settings")
	if err := os.WriteFile(foreign, []byte("preserve settings"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(runtimeDir, "sway.conf")
	if err := os.Symlink(foreign, target); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	path, err := infra.writeEmbeddedConfig("configs/headless.conf", image.Pt(1920, 1080))
	// Rejecting the unsafe path is acceptable, as is safely replacing only
	// the symlink itself. Following the symlink is never acceptable.
	data, readErr := os.ReadFile(foreign)
	if readErr != nil || string(data) != "preserve settings" {
		t.Fatalf("generated compositor config changed foreign file %q: %v", data, readErr)
	}
	if err == nil {
		if path != target {
			t.Fatalf("unexpected config path: %q", path)
		}
		info, statErr := os.Lstat(path)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unsafe generated config: %+v %v", info, statErr)
		}
		saved, readErr := os.ReadFile(path)
		if readErr != nil || !strings.Contains(string(saved), "resolution 1920x1080") {
			t.Fatalf("wrong isolated config: %v", readErr)
		}
	}
}

// PID metadata is equally sensitive: process startup is not authorized to
// change arbitrary user files through a symlink with the expected PID name.
func TestManagedChildPIDWriteCannotClobberSymlinkTarget(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreign := filepath.Join(t.TempDir(), "user-file")
	if err := os.WriteFile(foreign, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(runtimeDir, "child.pid")
	if err := os.Symlink(foreign, pidfile); err != nil {
		t.Fatal(err)
	}
	(&sessionInfra{xdgDir: runtimeDir}).writeChildPID("child.pid", os.Getpid())
	saved, readErr := os.ReadFile(foreign)
	if readErr != nil || string(saved) != "preserve" {
		t.Fatalf("PID writer changed foreign file %q: %v", saved, readErr)
	}
}

// PID filenames are not a path API. Even a syntactically valid relative path
// must never escape the directory where this session owns metadata.
func TestManagedChildPIDWriteRejectsTraversalAndEmbeddedPath(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreignDir, err := os.MkdirTemp(filepath.Dir(runtimeDir), "perfuncted-owned-outside-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(foreignDir) })
	foreign := filepath.Join(foreignDir, "foreign.pid")
	if err := os.WriteFile(foreign, []byte("do not replace"), 0600); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	for _, name := range []string{"", ".", "..", "nested/child.pid", "../" + filepath.Base(foreignDir) + "/foreign.pid", "child\x00.pid"} {
		infra.writeChildPID(name, os.Getpid())
		got, readErr := os.ReadFile(foreign)
		if readErr != nil || string(got) != "do not replace" {
			t.Fatalf("unsafe pid name %q changed foreign file: %q %v", name, got, readErr)
		}
	}
	if entries, readErr := os.ReadDir(runtimeDir); readErr != nil || len(entries) != 0 {
		t.Fatalf("invalid PID paths left runtime files: %v %v", entries, readErr)
	}
}

// Existing hard links must not be modified in place: the writer must publish
// a fresh inode rather than opening a foreign inode through an alias.
func TestManagedSessionFileWritesReplaceOnlyOwnedDirectoryEntry(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	foreign := filepath.Join(runtimeDir, "foreign")
	if err := os.WriteFile(foreign, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(runtimeDir, "child.pid")
	if err := os.Link(foreign, alias); err != nil {
		t.Skipf("hardlink unavailable: %v", err)
	}
	infra := &sessionInfra{xdgDir: runtimeDir}
	infra.writeChildPID("child.pid", os.Getpid())
	retained, readErr := os.ReadFile(foreign)
	if readErr != nil || string(retained) != "retain" {
		t.Fatalf("write changed hardlink sibling: %q %v", retained, readErr)
	}
	pid, pidErr := sessionruntime.ReadPIDFile(alias)
	if pidErr != nil || pid != os.Getpid() {
		t.Fatalf("fresh PID metadata unavailable: %d %v", pid, pidErr)
	}
	info, statErr := os.Stat(alias)
	if statErr != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("PID file mode not private: %v %v", info, statErr)
	}
	matches, globErr := filepath.Glob(filepath.Join(runtimeDir, ".perfuncted-managed-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("pending atomic writes leaked: %v %v", matches, globErr)
	}
}

type signalCleanupLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	notify chan struct{}
}

func (l *signalCleanupLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	n, err := l.buffer.Write(p)
	l.mu.Unlock()
	select {
	case l.notify <- struct{}{}:
	default:
	}
	return n, err
}

func (l *signalCleanupLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

// The signal handler must report teardown failure and retain a runtime whose
// permissions were changed, rather than deleting potentially unowned data.
func TestSessionSignalCleanupRetainsUnsafeRuntimeDirectory(t *testing.T) {
	const childEnv = "PF_TEST_SIGNAL_CLEANUP_FAILURE"
	const dirEnv = "PF_TEST_SIGNAL_CLEANUP_FAILURE_DIR"
	if os.Getenv(childEnv) != "1" {
		dir := filepath.Join(testutil.ShortRuntimeDir(t), "unsafe-runtime")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		args := []string{"-test.run=^TestSessionSignalCleanupRetainsUnsafeRuntimeDirectory$", "-test.timeout=8s"}
		if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
			args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		cmd.Env = append(os.Environ(), childEnv+"=1", dirEnv+"="+dir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated signal cleanup: %v\n%s", err, output)
		}
		return
	}

	dir := os.Getenv(dirEnv)
	if dir == "" {
		t.Fatal("missing isolated runtime path")
	}
	marker := dir + "/preserve"
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: dir, stopDone: make(chan struct{})}
	logs := &signalCleanupLog{notify: make(chan struct{}, 1)}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	defer slog.SetDefault(previous)
	stop := infra.CleanupOnSignal(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-infra.stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("signal did not finish automatic cleanup")
	}
	if infra.stopErr == nil || !strings.Contains(infra.stopErr.Error(), "refuse unsafe managed runtime") {
		t.Fatalf("unsafe cleanup error = %v", infra.stopErr)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "keep" {
		t.Fatalf("unsafe runtime contents were changed: data=%q err=%v", data, err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		text := logs.text()
		if strings.Contains(text, "skip removal of runtime") && strings.Contains(text, "signal-triggered cleanup failed") {
			break
		}
		select {
		case <-logs.notify:
		case <-deadline.C:
			t.Fatalf("missing cleanup diagnostics: %s", logs.text())
		}
	}
}

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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	if session.windows.backend() != nil || session.outputs.Backend() != nil {
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	gotWindow, err := session.openCapabilityContext(context.Background(), CapabilityWindows)
	if err != nil || gotWindow != windows || session.windows.backend() != windows {
		t.Fatalf("window opener did not install exact backend: backend=%v err=%v", gotWindow, err)
	}
	if outputsOpened != 0 || session.outputs.Backend() != nil {
		t.Fatal("opening windows unexpectedly initialized the output backend")
	}
	gotOutput, err := session.openCapabilityContext(context.Background(), CapabilityOutputs)
	if err != nil || gotOutput != outputs || session.outputs.Backend() != outputs {
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
	session := &sessionCore{}
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
	session := &sessionCore{}
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
	var nilSession *sessionCore
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

// CleanupSessionLogs is an exported maintenance entry point. Unlike normal
// session startup, callers can supply a symlink root: cleanup must not follow
// it into a separate tree and delete unrelated directories there.
func TestCleanupSessionLogsNeverFollowsSymlinkRoot(t *testing.T) {
	base := t.TempDir()
	external := filepath.Join(base, "external")
	if err := os.Mkdir(external, 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(external, sessionruntime.SessionLogPrefix+"expired")
	if err := os.Mkdir(victim, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("do-not-delete"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "log-alias")
	if err := os.Symlink(external, alias); err != nil {
		t.Fatal(err)
	}
	CleanupSessionLogs(alias, 24*time.Hour)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("followed symlink root and deleted external log: %v", err)
	}
}

// A session creates its own fresh log directory and must never truncate an
// existing path, including a symlink or a hard-linked file, even if its log
// directory was unexpectedly tampered with.
func TestSessionLogCreationNeverTruncatesPreexistingTargets(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "regular"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "private-data")
			const original = "session-private-content-must-not-be-lost"
			if err := os.WriteFile(target, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(dir, "sway-session.log")
			switch mode {
			case "symlink":
				if err := os.Symlink(target, logPath); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, logPath); err != nil {
					t.Fatal(err)
				}
			case "regular":
				if err := os.WriteFile(logPath, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
			}
			file, err := sessionruntime.OpenPrivateSessionLog(logPath)
			if file != nil {
				_ = file.Close()
			}
			if err == nil {
				t.Fatal("reused existing log path instead of failing closed")
			}
			got, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != original {
				t.Fatalf("existing target truncated/modified: got %q", got)
			}
		})
	}
}

func TestRejectedSwayStartupDoesNotPoisonFreshLogDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	infra := &sessionInfra{logDir: dir, ctx: context.Background()}
	logPath := filepath.Join(dir, "sway-session.log")
	for _, mode := range []sessionMode{sessionMode(12345), sessionModeNested} {
		if err := infra.launchSway(context.Background(), filepath.Join(dir, "unused.conf"), mode); err == nil {
			t.Fatalf("unexpected success for invalid/unavailable mode %v", mode)
		}
		if _, err := os.Lstat(logPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rejected mode %v created/modified log path: %v", mode, err)
		}
	}
}

// A managed log root may become unreadable while retaining private session
// directories. Retention must not infer that those directories are absent,
// follow a fallback path, or modify an unrelated root.
func TestSessionLogRetentionDoesNotTouchUnreadableRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory read permission")
	}
	root := filepath.Join(t.TempDir(), "private-retention")
	child := filepath.Join(root, sessionruntime.SessionLogPrefix+"retained")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(child, "private.log")
	if err := os.WriteFile(payload, []byte("preserve private audit"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(child, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(root, 0700); err != nil {
			t.Errorf("restore log root permissions: %v", err)
		}
	})
	CleanupSessionLogs(root, time.Hour)
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(payload)
	if err != nil || string(got) != "preserve private audit" {
		t.Fatalf("unreadable retention root lost audit data: got=%q err=%v", got, err)
	}
}

// The raw sessionruntime.Group path owns process-group shutdown without an asynchronous
// reaper. A leader that ignores SIGTERM must be force-killed and reaped, not
// reported stopped merely because the TERM grace elapsed.
func TestManagedProcDirectStopEscalatesAndReapsTermResistantLeader(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready, "GO_WANT_IGNORE_SIGTERM=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = cmd.Wait() })
	if err := sessionruntime.WaitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	proc := sessionruntime.NewGroup(cmd, pid)
	if !proc.GroupAlive() {
		t.Fatal("running helper group was not detected")
	}
	proc.Stop(60 * time.Millisecond)
	if proc.GroupAlive() || sessionruntime.ProcessGroupAlive(pid) {
		t.Fatal("SIGTERM-resistant helper survived direct stop escalation")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("direct stop failed to reap helper pid=%d: %v", pid, err)
	}
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("direct stop left waitable process: %v", err)
	}
	// Repeated stop must not signal an unrelated process that inherited the PID.
	proc.Stop(0)
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("signal after reap: %v", err)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("helper never initialized handler: %v", err)
	}
}

// A nil parent context is a supported call path and must be replaced by a
// bounded background context rather than passed to context.WithTimeout.
func TestCapabilityOpeningNilParentStillUsesBoundedContext(t *testing.T) {
	backend := &trackedCapabilityBackend{}
	installed := false
	got, err := openCapabilityBackend[*trackedCapabilityBackend](nil, time.Second, CapabilityScreen,
		func(ctx context.Context) (*trackedCapabilityBackend, error) {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) <= 0 {
				return nil, errors.New("nil parent did not receive a live startup deadline")
			}
			return backend, nil
		}, nil,
		func(value *trackedCapabilityBackend) { installed = value == backend },
	)
	if err != nil || got != backend || !installed || backend.closes != 0 {
		t.Fatalf("nil-parent capability result %v, err=%v installed=%v closes=%d", got, err, installed, backend.closes)
	}
}

// Capability setup can be called before a screen backend exists. No timeout
// mutation may be attempted on a missing backend.
func TestConfigureScreenTimeoutAcceptsMissingBackend(t *testing.T) {
	configureScreenBackendTimeout(nil, time.Second)
}

// PID 0 and negative IDs must never be used as liveness evidence: kill(0,0)
// targets a whole process group and has different authorization semantics.
func TestSessionPIDLivenessRejectsNonpositiveIdentifiers(t *testing.T) {
	for _, pid := range []int{0, -1, -1024} {
		if sessionruntime.PIDAlive(pid) {
			t.Fatalf("invalid process identifier %d accepted as live", pid)
		}
	}
}

// A missing clipboard facade must not initiate a desktop write.
func TestNilClipboardFacadeCannotWrite(t *testing.T) {
	var clipboard *clipboardState
	if err := clipboard.Set(context.Background(), "private text"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil clipboard Set error %v, want ErrNilSession", err)
	}
}

// Optional nil configuration entries must not prevent a host session from
// opening, and explicitly enabled action traces must use the caller's writer.
func TestOpenIgnoresNilOptionAndRoutesOwnedTrace(t *testing.T) {
	var writer bytes.Buffer
	session, err := openSession(context.Background(), nil, WithTrace(true), WithTraceOut(&writer))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.tracer == nil {
		t.Fatal("requested trace writer did not initialize")
	}
	session.inputAdmission.traceAction("input", "key-up %s", "Return")
	if got := writer.String(); !strings.Contains(got, "input key-up Return") {
		t.Fatalf("session did not route trace to configured writer: %q", got)
	}
	if session.Has(CapabilityInput) {
		t.Fatal("an unrequested input backend became available")
	}
}

// An invalid context must be refused before configuration or desktop routing.
func TestOpenNilContextDoesNotEvaluateOptions(t *testing.T) {
	called := false
	session, err := openSession(nil, Option(func(*openConfig) error {
		called = true
		return nil
	}))
	if session != nil || !errors.Is(err, ErrInvalidArgument) || called {
		t.Fatalf("nil-context Open returned session=%v err=%v evaluatedOptions=%v", session, err, called)
	}
}

// Invalid configuration must stop option evaluation before any runtime or
// backend setup; a later option cannot turn an error into success.
func TestOpenOptionFailureStopsConfigurationBeforeRuntime(t *testing.T) {
	rejected := errors.New("synthetic rejected desktop configuration")
	laterCalled := false
	session, err := openSession(context.Background(),
		Option(func(*openConfig) error { return rejected }),
		Option(func(*openConfig) error { laterCalled = true; return nil }),
	)
	if session != nil || !errors.Is(err, rejected) || laterCalled {
		t.Fatalf("failed option opened session=%v err=%v laterCalled=%v", session, err, laterCalled)
	}
}

// A configuration option can end the owner's permission to start a desktop.
func TestOpenOptionCancellationStopsRuntimeSelection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := openSession(ctx, Option(func(*openConfig) error {
		cancel()
		return nil
	}))
	if session != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("configuration cancellation opened session=%v err=%v", session, err)
	}
}

// Closing a session revokes direct paste authority independently of whether
// clipboard and keyboard backends were ever requested.
func TestClosedSessionPasteRejectsWithoutDesktopDispatch(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Paste(context.Background(), "private selection"); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session accepted paste: %v", err)
	}
}

func TestTraceAndNestedOptionsKeepConfigurationAndRejectTargetCollision(t *testing.T) {
	cfg := &openConfig{}
	target := SessionConfig{}
	for _, option := range []Option{WithNested(target), WithTrace(true), WithTraceDelay(17 * time.Millisecond)} {
		if err := option(cfg); err != nil {
			t.Fatal(err)
		}
	}
	var destination bytes.Buffer
	if err := WithTraceOut(&destination)(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.target.kind != TargetNested || !cfg.selected || !cfg.trace || cfg.traceDelay != 17*time.Millisecond || cfg.traceOut != &destination {
		t.Fatalf("options lost nested mode or trace settings: %+v", cfg)
	}
	for _, option := range []Option{WithNested(target), WithHeadless(target)} {
		if err := option(cfg); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("duplicate managed target accepted: %v", err)
		}
	}
	if err := WithTrace(false)(cfg); err != nil || cfg.trace {
		t.Fatalf("trace opt-out ignored: %v", err)
	}
}

func TestCompositeWaitConditionPreservesEventWakeupsAndShortCircuits(t *testing.T) {
	ctx := context.Background()
	calls := make([]string, 0, 3)
	condition := func(name string, value bool, err error, pollOnly bool) Condition {
		return conditionFunc{name: name, pollOnly: pollOnly, check: func(context.Context, *sessionCore) (bool, error) { calls = append(calls, name); return value, err }}
	}
	observed := errors.New("synthetic unreadable window state")
	falseCondition := condition("false", false, nil, true)
	broken := condition("broken", false, observed, false)
	trueCondition := condition("true", true, nil, true)
	all := All(falseCondition, broken, trueCondition)
	if all.describe() != "all" || all.pollingOnly() {
		t.Fatal("all condition hid event-capable child")
	}
	if value, err := all.evaluate(ctx, nil); value || err != nil || strings.Join(calls, ",") != "false" {
		t.Fatalf("all did not short-circuit unfulfilled proof: value=%v err=%v calls=%v", value, err, calls)
	}
	calls = nil
	any := Any(broken, trueCondition, falseCondition)
	if any.describe() != "any" || any.pollingOnly() {
		t.Fatal("any condition suppressed event wakeups")
	}
	if value, err := any.evaluate(ctx, nil); !value || err != nil || strings.Join(calls, ",") != "broken,true" {
		t.Fatalf("independent positive proof lost: value=%v err=%v calls=%v", value, err, calls)
	}
	calls = nil
	allBroken := Any(broken, falseCondition)
	if value, err := allBroken.evaluate(ctx, nil); value || !errors.Is(err, observed) {
		t.Fatalf("all alternatives unreadable lost error: value=%v err=%v", value, err)
	}
	negative := Not(falseCondition)
	if negative.describe() != "not false" || !negative.pollingOnly() {
		t.Fatal("not condition changed polling classification")
	}
	if value, err := negative.evaluate(ctx, nil); !value || err != nil {
		t.Fatalf("not false result=%v err=%v", value, err)
	}
	if desc := Not(nil).describe(); desc != "not <nil>" {
		t.Fatalf("nil child described as %q", desc)
	}
	if Not(nil).pollingOnly() {
		t.Fatal("nil child claimed polling readiness")
	}
	if _, err := Not(nil).evaluate(ctx, nil); err == nil {
		t.Fatal("nil child condition evaluated as success")
	}
	if !All(trueCondition, falseCondition).pollingOnly() {
		t.Fatal("poll-only composite classified as event-capable")
	}
}

// Runtime setup and PID publication are distinct startup phases. A replaced
// owner PID entry must not redirect startup's write to an unrelated file.
func TestSessionStartupOwnerPIDWriteDoesNotFollowSymlink(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfowner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("TMPDIR", root)
	foreign := filepath.Join(t.TempDir(), "unrelated-user-file")
	const contents = "foreign private content must remain untouched"
	if err := os.WriteFile(foreign, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	var runtimeDir string
	var fixtureErr error
	s := &sessionCore{startupObserver: func(phase StartupPhase, state StartupPhaseState) {
		if phase != StartupPhaseRuntimeDirectoryPreparation || state != StartupPhaseSucceeded {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			fixtureErr = err
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
				continue
			}
			runtimeDir = filepath.Join(root, entry.Name())
			fixtureErr = os.Symlink(foreign, filepath.Join(runtimeDir, sessionOwnerPIDFile))
			return
		}
	}}
	infra, startErr := s.startSession(context.Background(), sessionModeHeadless, SessionConfig{LogDir: filepath.Join(root, "logs")}, false)
	if fixtureErr != nil {
		t.Fatalf("test setup: %v", fixtureErr)
	}
	if runtimeDir == "" {
		t.Fatal("startup never published a managed runtime")
	}
	if infra != nil {
		t.Cleanup(func() { _ = infra.stop() })
	}
	_ = startErr // The installed desktop helper may be unavailable in tests.
	data, err := os.ReadFile(foreign)
	if err != nil || string(data) != contents {
		t.Fatalf("owner PID publication overwrote unrelated file: %q %v", data, err)
	}
}

// A missing caller context must be rejected before any clipboard or keyboard
// backend can dispatch a paste. Context.Background is not an implicit grant.
func TestSessionPasteRejectsNilContextBeforeDispatch(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	if err := session.Paste(nil, "private selection"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("paste without explicit context = %v, want ErrInvalidArgument", err)
	}
}

// An absent or only partially initialized owner never authorizes operations.
func TestSessionAbsentReceiverAndOwnerlessTraceFailClosed(t *testing.T) {
	var session *sessionCore
	if !session.isClosed() {
		t.Fatal("nil session must be treated as closed")
	}
	var base *bundleBase
	base.traceAction("clipboard", "set") // Must not dereference a missing owner.
	orphan := &bundleBase{}
	orphan.traceAction("clipboard", "set")
}

// The direct managed-session stop path must terminate and reap a real helper;
// an interrupted waiter cannot assume ownership of the reap or release the
// process group while it is still running.
func TestManagedSessionDirectStopTerminatesAndReapsActiveHelper(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := sessionruntime.NewSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	if err := sessionruntime.WaitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if process.WaitTimeout(0) {
		t.Fatal("running helper prematurely marked reaped")
	}
	if process.WaitTimeout(10 * time.Millisecond) {
		t.Fatal("running helper completed during timeout")
	}
	process.Stop(250 * time.Millisecond)
	if !process.WaitTimeout(0) {
		t.Fatal("direct stop returned without reaping helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.Wait(ctx, ErrInvalidArgument); err == nil {
		t.Fatal("terminated helper reported successful exit")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("terminated helper still exists: %v", err)
	}
	// Repeated stops must not send signals to a recycled process identity.
	process.Stop(0)
}

func TestManagedSessionWaitPropagatesActualNonzeroExitAndReapsOnce(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 23")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := sessionruntime.NewSessionProcess(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := process.Wait(ctx, ErrInvalidArgument)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("actual child exit status lost: %v", err)
	}
	if !process.WaitTimeout(0) {
		t.Fatal("exit published before reap completed")
	}
	if err2 := process.Wait(ctx, ErrInvalidArgument); !errors.As(err2, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("second waiter did not see same result: %v", err2)
	}
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WNOHANG, nil); !errors.Is(err, syscall.ECHILD) {
		t.Fatalf("child not reaped exactly once: %v", err)
	}
}

func TestManagedSessionProcessWaitCancellationKeepsReapingChild(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+readyPath)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start managed process: %v", err)
	}
	process := sessionruntime.NewSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })

	if err := sessionruntime.WaitForFile(context.Background(), readyPath, 100, 5*time.Millisecond); err != nil {
		t.Fatalf("wait for helper readiness: %v", err)
	}
	if process.WaitTimeout(-time.Second) {
		t.Fatal("negative timeout reported an unfinished child as exited")
	}

	if err := process.Wait(nil, ErrInvalidArgument); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("wait with nil context = %v, want ErrInvalidArgument", err)
	}
	waitCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := process.Wait(waitCtx, ErrInvalidArgument); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait with canceled context = %v, want context.Canceled", err)
	}
	if process.WaitTimeout(0) {
		t.Fatal("canceled waiter released ownership before the child was reaped")
	}

	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("terminate managed process group: %v", err)
	}
	completionCtx, completionCancel := context.WithTimeout(context.Background(), time.Second)
	defer completionCancel()
	if err := process.Wait(completionCtx, ErrInvalidArgument); err == nil {
		t.Fatal("reaped helper process unexpectedly returned a successful exit")
	}
	if !process.WaitTimeout(0) {
		t.Fatal("managed process did not publish completed wait")
	}
}

// A helper that deliberately ignores SIGTERM must still be killed and reaped
// by the direct managed-session shutdown path.
func TestManagedSessionStopEscalatesAfterIgnoredSIGTERM(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := helperCommand(t)
	cmd.Env = env.Merge(cmd.Env, "GO_HELPER_READY="+ready, "GO_WANT_IGNORE_SIGTERM=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := sessionruntime.NewSessionProcess(cmd)
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	if err := sessionruntime.WaitForFile(context.Background(), ready, 100, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// The helper has installed the ignore handler before reporting ready.
	if _, err := os.Stat(ready); err != nil {
		t.Fatal(err)
	}
	process.Stop(30 * time.Millisecond)
	if !process.WaitTimeout(0) {
		t.Fatal("stop returned without reaping SIGTERM-resistant helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := process.Wait(ctx, ErrInvalidArgument); err == nil {
		t.Fatal("forced termination reported success")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("helper still exists after stop escalation: %v", err)
	}
	process.Stop(0)
}

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
	_, err := openSession(context.Background(),
		WithTarget(EnvironmentTarget(nil)),
		WithRemoteDesktopInput(input.RemoteDesktopOptions{ParentWindow: "xdg:test-parent", PersistPermission: true}),
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
	options := input.RemoteDesktopOptions{}
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
	root := testutil.ShortSocketDir(t)
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

func TestStartSessionCancellationAfterSwayReadinessStopsProcessesAndCleansRuntime(t *testing.T) {
	root := prepareHeadlessHelpers(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var runtimeDir string
	var processIDs []int
	var observerErr error
	var events []startupEvent
	session := &sessionCore{startupObserver: func(phase StartupPhase, state StartupPhaseState) {
		events = append(events, startupEvent{phase: phase, state: state})
		if phase != StartupPhaseSwaySocketsReady || state != StartupPhaseSucceeded {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			observerErr = fmt.Errorf("inspect synthetic runtime root: %w", err)
			cancel()
			return
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
				runtimeDir = filepath.Join(root, entry.Name())
				break
			}
		}
		if runtimeDir == "" {
			observerErr = errors.New("ready managed runtime directory is missing")
			cancel()
			return
		}
		for _, name := range []string{"dbus.pid", "sway.pid"} {
			data, readErr := os.ReadFile(filepath.Join(runtimeDir, name))
			if readErr != nil {
				observerErr = fmt.Errorf("read %s: %w", name, readErr)
				cancel()
				return
			}
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr != nil {
				observerErr = fmt.Errorf("parse %s: %w", name, parseErr)
				cancel()
				return
			}
			processIDs = append(processIDs, pid)
		}
		cancel()
	}}

	infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{
		Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond},
	}, false)
	if infra != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("startup after compositor readiness returned infra=%v error=%v, want nil infrastructure and cancellation", infra, err)
	}
	if observerErr != nil {
		t.Fatal(observerErr)
	}
	if !hasStartupEvent(events, startupEvent{StartupPhaseSwaySocketsReady, StartupPhaseSucceeded}) {
		t.Fatalf("startup did not reach compositor readiness: %+v", events)
	}
	if len(processIDs) != 2 {
		t.Fatalf("recorded managed process IDs=%v, want D-Bus and Sway", processIDs)
	}
	for _, pid := range processIDs {
		if sessionruntime.PIDAlive(pid) {
			t.Errorf("managed process %d survived startup cancellation", pid)
		}
	}
	requireNoManagedSessionDirs(t, root)
}

// A startup error and its cleanup error are both authoritative. The caller
// must not be told only about the launch failure while a managed runtime
// remains on disk and may still have resources attached.
func TestStartSessionFailureReportsCleanupError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires ordinary filesystem permissions")
	}
	root, err := os.MkdirTemp("/var/tmp", "pft-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	t.Setenv("TMPDIR", root)

	var managed string
	var prepErr error
	s := newSessionForTesting(nil, nil, nil, nil, nil)
	s.startupObserver = func(phase StartupPhase, state StartupPhaseState) {
		if phase != StartupPhaseRuntimeDirectoryPreparation || state != StartupPhaseSucceeded {
			return
		}
		entries, listErr := os.ReadDir(root)
		if listErr != nil {
			prepErr = listErr
			return
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
				continue
			}
			managed = filepath.Join(root, entry.Name())
			// The sentinel makes removal require write permission *inside*
			// the runtime directory, unlike removing an empty directory.
			if prepErr = os.WriteFile(filepath.Join(managed, "sentinel"), []byte("retain"), 0600); prepErr != nil {
				return
			}
			prepErr = os.Chmod(managed, 0500)
			return
		}
	}
	defer func() {
		if managed != "" {
			_ = os.Chmod(managed, 0700)
			_ = os.RemoveAll(managed)
		}
	}()
	infra, err := s.startSession(context.Background(), sessionModeHeadless,
		SessionConfig{LogDir: filepath.Join(root, "logs")}, false)
	if prepErr != nil {
		t.Fatalf("fixture setup: %v", prepErr)
	}
	if managed == "" {
		t.Fatal("startup did not produce an isolated managed runtime")
	}
	if infra != nil {
		t.Fatal("failed startup returned a live session")
	}
	if err == nil || !strings.Contains(err.Error(), "write owner pidfile") {
		t.Fatalf("startSession error=%v, expected forced write failure", err)
	}
	if !strings.Contains(err.Error(), "remove managed runtime") {
		t.Fatalf("startSession lost its cleanup failure: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(managed, "sentinel")); statErr != nil {
		t.Fatalf("fixture no longer exhibits cleanup failure: %v", statErr)
	}
}

func TestManagedStartupCleansUpWhenSwayConfigCannotBePublished(t *testing.T) {
	root := prepareHeadlessHelpers(t, true)
	var setupErr error
	var events []startupEvent
	session := &sessionCore{startupObserver: func(phase StartupPhase, state StartupPhaseState) {
		events = append(events, startupEvent{phase: phase, state: state})
		if phase != StartupPhaseSwayConfiguration || state != StartupPhaseStarted {
			return
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			setupErr = err
			return
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
				setupErr = os.Mkdir(filepath.Join(root, entry.Name(), "sway.conf"), 0700)
				return
			}
		}
		setupErr = errors.New("managed runtime not found")
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{
		Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond},
	}, false)
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	if infra != nil || err == nil || !strings.Contains(err.Error(), "write config") {
		t.Fatalf("startup with conflicting config destination = (%v, %v)", infra, err)
	}
	if !hasStartupEvent(events, startupEvent{StartupPhaseSwayConfiguration, StartupPhaseFailed}) {
		t.Fatalf("missing configuration failure event: %+v", events)
	}
	for _, event := range events {
		if event.phase == StartupPhaseSwayProcessSpawn {
			t.Fatalf("Sway started after config failure: %+v", events)
		}
	}
	requireNoManagedSessionDirs(t, root)
}

// An explicitly supplied Sway config must not be replaced by an embedded
// compositor config or copied into the managed runtime. Cancellation must
// still prevent configuration selection, including explicit overrides.
func TestManagedSessionExplicitSwayConfigPreservesCallerFileAndCancellation(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	callerConfig := filepath.Join(t.TempDir(), "user-sway.conf")
	const original = "# user selected config\noutput * resolution 1280x720\n"
	if err := os.WriteFile(callerConfig, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	session := &sessionCore{}
	infra := &sessionInfra{xdgDir: runtimeDir}
	config := SessionConfig{SwayConfigPath: callerConfig, Resolution: image.Pt(1920, 1080)}
	for _, mode := range []sessionMode{sessionModeHeadless, sessionModeNested} {
		got, err := session.resolveSessionSwayConfig(context.Background(), infra, mode, config)
		if err != nil || got != callerConfig {
			t.Fatalf("explicit config mode %d: path=%q error=%v", mode, got, err)
		}
	}
	if data, err := os.ReadFile(callerConfig); err != nil || string(data) != original {
		t.Fatalf("caller configuration mutated: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "sway.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unrequested generated config exists: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := session.resolveSessionSwayConfig(ctx, infra, sessionModeHeadless, config); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled config selected: %q %v", got, err)
	}
}

// Managed runtime paths are literal. Glob metacharacters in their ancestors
// must not redirect socket discovery to another session or hide a real socket.
func TestSwaySocketUsesLiteralRuntimeDirectory(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pf-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	own := filepath.Join(root, "private[ab]")
	foreign := filepath.Join(root, "privatea")
	for _, dir := range []string{own, foreign} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	listen := func(dir string) (string, net.Listener) {
		path := filepath.Join(dir, "sway-ipc.123.sock")
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		return path, listener
	}
	outside, foreignListener := listen(foreign)
	defer foreignListener.Close()
	expected, ownListener := listen(own)
	defer ownListener.Close()
	session := &sessionCore{infra: &sessionInfra{xdgDir: own}}
	if got := session.SwaySocket(); got != expected {
		t.Fatalf("runtime %q resolved socket %q, want own %q (foreign %q)", own, got, expected, outside)
	}
}

func TestSwaySocketSupportsInvalidGlobCharacterInLiteralRuntime(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfsock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	own := filepath.Join(root, "private[unfinished")
	if err := os.Mkdir(own, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(own, "sway-ipc.local.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	session := &sessionCore{infra: &sessionInfra{xdgDir: own}}
	if got := session.SwaySocket(); got != path {
		t.Fatalf("literal bracket runtime socket=%q want %q", got, path)
	}
}

// Missing runtime directories must not cause discovery to fall back to the
// caller's desktop, nor should an invalid lookalike be accepted as Sway IPC.
func TestSwaySocketRejectsMissingRuntimeAndMalformedSocketNames(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pf-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	missing := &sessionCore{infra: &sessionInfra{xdgDir: filepath.Join(root, "missing")}}
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
	owned := &sessionCore{infra: &sessionInfra{xdgDir: runtime}}
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
	session := &sessionCore{infra: &sessionInfra{xdgDir: runtimeDir}}
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	session := newSessionForTesting(nil, nil, nil, nil, nil)
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
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	failure := errors.New("synthetic window discovery connection timeout")
	manager.setError(failure)
	match := window.Match{TitleExact: "private title"}
	got, err := session.windows.Find(context.Background(), match)
	if got != nil || !errors.Is(err, failure) {
		t.Fatalf("failed discovery accepted window=%v err=%v", got, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	got, err = session.windows.Wait(ctx, match, WaitEvery(time.Millisecond))
	if got != nil || !errors.Is(err, failure) {
		t.Fatalf("unreadable backend yielded window=%v err=%v", got, err)
	}
	manager.setError(nil)
	got, err = session.windows.Wait(context.Background(), match)
	if err != nil || got == nil || got.ID().String() != "stable-1" {
		t.Fatalf("recovered backend could not discover stable window: window=%v err=%v", got, err)
	}
}

// A failed stable-ID metadata read must not corrupt the cached window handle.
// Once the manager recovers, the same handle can accept a fresh observation.
func TestWindowInfoTransportFailurePreservesCacheAndLaterRecovers(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "stable-id", Title: "original"})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := session.windows.window(window.Info{NativeID: "stable-id", Title: "original"})
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
	var missing *windowState
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
	if _, err := (&windowState{}).Info(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned window Info returned %v", err)
	}
	var windows WindowBundle
	if got, err := windows.Wait(context.Background(), window.Match{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil window bundle wait=%v %v", got, err)
	}
	if got, err := (&windowBundleState{}).Wait(context.Background(), window.Match{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned window bundle wait=%v %v", got, err)
	}
	var app *applicationState
	if got, err := app.WaitForWindow(context.Background(), window.Match{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil app wait=%v %v", got, err)
	}
	if got, err := (&applicationState{}).WaitForWindow(context.Background(), window.Match{}); got != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned app wait=%v %v", got, err)
	}
}

// Window mutations are scoped to a session-backed stable handle. An absent
// owner must reject every operation before attempting desktop input or
// accidentally forwarding an empty native identifier to a compositor.
func TestUnownedWindowMutationEntryPointsRejectBeforeDispatch(t *testing.T) {
	ctx := context.Background()
	operations := []struct {
		name string
		run  func(*windowState) error
	}{
		{"activate", func(w *windowState) error { return w.Activate(ctx) }},
		{"move", func(w *windowState) error { return w.Move(ctx, 10, 20) }},
		{"resize", func(w *windowState) error { return w.Resize(ctx, 640, 480) }},
		{"close", func(w *windowState) error { return w.Close(ctx) }},
		{"minimize", func(w *windowState) error { return w.Minimize(ctx) }},
		{"maximize", func(w *windowState) error { return w.Maximize(ctx) }},
		{"fullscreen", func(w *windowState) error { return w.Fullscreen(ctx) }},
		{"unfullscreen", func(w *windowState) error { return w.Unfullscreen(ctx) }},
		{"restore", func(w *windowState) error { return w.Restore(ctx) }},
	}
	for _, owner := range []struct {
		name   string
		handle *windowState
	}{
		{name: "nil handle"},
		{name: "unowned handle", handle: &windowState{}},
	} {
		t.Run(owner.name, func(t *testing.T) {
			for _, op := range operations {
				t.Run(op.name, func(t *testing.T) {
					if err := op.run(owner.handle); !errors.Is(err, ErrNilSession) {
						t.Fatalf("unowned window operation accepted: %v", err)
					}
				})
			}
		})
	}
}

// A stable window handle must not mistake a title change for closure, and a
// canceled waiter must never manufacture a successful disappearance. Later
// verified disappearance of the same native ID must complete a fresh wait.
func TestWindowWaitClosedHonorsStableIdentityAndWaiterCancellation(t *testing.T) {
	var absent *windowState
	if err := absent.WaitClosed(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil handle wait=%v", err)
	}
	if err := (&windowState{}).WaitClosed(context.Background()); !errors.Is(err, ErrNilSession) {
		t.Fatalf("unowned handle wait=%v", err)
	}
	manager := newHandleWindowManager(window.Info{NativeID: "native-17", Title: "Original"})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	handle := &windowState{id: WindowID{session: session, native: "native-17"}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handle.WaitClosed(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled waiter=%v", err)
	}
	manager.setWindows(false, window.Info{NativeID: "native-17", Title: "Renamed"})
	ctx, stop := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer stop()
	if err := handle.WaitClosed(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("renamed live window incorrectly considered closed: %v", err)
	}
	manager.setWindows(true)
	doneCtx, doneCancel := context.WithTimeout(context.Background(), time.Second)
	defer doneCancel()
	if err := handle.WaitClosed(doneCtx); err != nil {
		t.Fatalf("verified native-ID disappearance not accepted: %v", err)
	}
}

func installNestedStartupHelpers(t *testing.T) string {
	t.Helper()

	runtimeRoot := prepareHeadlessHelpers(t, false)
	bin := filepath.Join(runtimeRoot, "bin")
	dbusScript := filepath.Join(runtimeRoot, "synthetic-dbus.py")
	swayScript := filepath.Join(runtimeRoot, "synthetic-sway.py")
	for path, contents := range map[string]string{
		dbusScript: "import os, signal, socket\n" +
			"listener = socket.socket(socket.AF_UNIX)\n" +
			"listener.bind(os.path.join(os.environ['XDG_RUNTIME_DIR'], 'bus'))\n" +
			"listener.listen(1)\n" +
			"signal.pause()\n",
		swayScript: "import os, signal, socket\n" +
			"listeners = []\n" +
			"for name in ('wayland-1', 'sway-ipc.synthetic.sock'):\n" +
			"    listener = socket.socket(socket.AF_UNIX)\n" +
			"    listener.bind(os.path.join(os.environ['XDG_RUNTIME_DIR'], name))\n" +
			"    listener.listen(1)\n" +
			"    listeners.append(listener)\n" +
			"signal.pause()\n",
		filepath.Join(bin, "dbus-daemon"): "#!/bin/sh\nset -eu\nexec python3 \"$PF_SYNTH_DBUS_HELPER\"\n",
		filepath.Join(bin, "sway"): "#!/bin/sh\nset -eu\n" +
			"test \"$WAYLAND_DISPLAY\" = \"$PF_EXPECTED_HOST_WAYLAND\"\n" +
			"test \"$WLR_BACKENDS\" = wayland\n" +
			"test \"$WLR_RENDERER\" = pixman\n" +
			"exec python3 \"$PF_SYNTH_SWAY_HELPER\"\n",
		filepath.Join(bin, "wl-paste"): "#!/bin/sh\nset -eu\nexec python3 -c 'import signal; signal.pause()'\n",
	} {
		mode := os.FileMode(0o600)
		if strings.HasPrefix(contents, "#!/") {
			mode = 0o700
		}
		if err := os.WriteFile(path, []byte(contents), mode); err != nil {
			t.Fatalf("write nested startup helper %q: %v", path, err)
		}
	}
	t.Setenv("PF_SYNTH_DBUS_HELPER", dbusScript)
	t.Setenv("PF_SYNTH_SWAY_HELPER", swayScript)
	return runtimeRoot
}

func TestOpenNestedRuntimeUsesHostWaylandAndOwnsManagedChildren(t *testing.T) {
	root := installNestedStartupHelpers(t)
	hostRuntime := testutil.ShortSocketDir(t)
	hostSocket := filepath.Join(hostRuntime, "wayland-host")
	listener, err := net.Listen("unix", hostSocket)
	if err != nil {
		t.Fatalf("listen on synthetic host Wayland socket: %v", err)
	}
	defer listener.Close()
	t.Setenv("XDG_RUNTIME_DIR", hostRuntime)
	t.Setenv("WAYLAND_DISPLAY", filepath.Base(hostSocket))
	t.Setenv("PF_EXPECTED_HOST_WAYLAND", hostSocket)
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := openSession(ctx, WithNested(SessionConfig{Timeouts: TimeoutPolicy{Startup: 3 * time.Second, Poll: time.Millisecond}}))
	if err != nil {
		t.Fatalf("Open nested runtime with a live host Wayland socket: %v", err)
	}
	managedRuntime := session.XDG()
	if session.Target().Kind() != TargetNested || filepath.Clean(managedRuntime) == filepath.Clean(hostRuntime) {
		t.Fatalf("nested target routing = kind %q runtime %q, host runtime %q", session.Target().Kind(), managedRuntime, hostRuntime)
	}
	if session.WaylandDisplay() != "wayland-1" || !strings.HasPrefix(session.DBusAddress(), "unix:path="+managedRuntime+string(os.PathSeparator)) {
		t.Fatalf("nested session did not publish its managed desktop endpoints: wayland=%q bus=%q runtime=%q", session.WaylandDisplay(), session.DBusAddress(), managedRuntime)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close nested runtime children: %v", err)
	}
	if _, err := os.Stat(managedRuntime); !os.IsNotExist(err) {
		t.Fatalf("managed nested runtime still exists after Close: %v", err)
	}
	requireNoManagedSessionDirs(t, root)
}

func TestOpenNestedRuntimeWithoutHostWaylandCleansUpStartedBus(t *testing.T) {
	root := installNestedStartupHelpers(t)
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	t.Setenv("PF_EXPECTED_HOST_WAYLAND", "")
	cleanupStaleSessionsMu.Lock()
	lastCleanupTime = time.Time{}
	cleanupStaleSessionsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := openSession(ctx, WithNested(SessionConfig{Timeouts: TimeoutPolicy{Startup: 3 * time.Second, Poll: time.Millisecond}}))
	if session != nil || err == nil || !strings.Contains(err.Error(), "nested session requires a host Wayland socket") {
		if session != nil {
			_ = session.Close()
		}
		t.Fatalf("nested startup without host Wayland = session %v, error %v", session, err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatalf("inspect synthetic runtime root: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
			t.Fatalf("failed nested startup leaked managed runtime %q", entry.Name())
		}
	}
}

// Every exported Session member must tolerate a nil receiver. Three accessors
// dereferenced the session directly and panicked, which turned a documented
// defensive case into a crash for library callers.
func TestNilSessionAccessorsDoNotPanic(t *testing.T) {
	var s *sessionCore

	stringAccessors := map[string]func() string{
		"XDG":            s.XDG,
		"DBusAddress":    s.DBusAddress,
		"WaylandDisplay": s.WaylandDisplay,
		"X11Display":     s.X11Display,
		"LogPath":        s.LogPath,
		"SwaySocket":     s.SwaySocket,
	}
	for name, accessor := range stringAccessors {
		t.Run(name, func(t *testing.T) {
			// A panic fails the test; the value must simply be empty.
			if got := accessor(); got != "" {
				t.Fatalf("%s() = %q, want the empty string", name, got)
			}
		})
	}

	t.Run("Env", func(t *testing.T) {
		if got := s.Env(); len(got) != 0 {
			t.Fatalf("Env() = %v, want empty", got)
		}
	})
	t.Run("Has", func(t *testing.T) {
		if s.Has(CapabilityScreen) {
			t.Fatal("Has() = true on a nil session")
		}
	})
	t.Run("Capability", func(t *testing.T) {
		if got := s.Capability(CapabilityScreen); got.Available || got.Requested {
			t.Fatalf("Capability() = %+v, want an unavailable status", got)
		}
	})
	t.Run("Capabilities", func(t *testing.T) {
		for _, status := range s.Capabilities() {
			if status.Available {
				t.Fatalf("Capabilities() reported %+v available", status)
			}
		}
	})
	t.Run("Close", func(t *testing.T) {
		if err := s.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
	})
	t.Run("Launch", func(t *testing.T) {
		_, err := s.Launch(context.Background(), Command{Name: "true"})
		if !errors.Is(err, ErrNilSession) {
			t.Fatalf("Launch() = %v, want ErrNilSession", err)
		}
	})
	t.Run("Paste", func(t *testing.T) {
		if err := s.Paste(context.Background(), "text"); err == nil {
			t.Fatal("Paste() = nil, want an error")
		}
	})
	t.Run("Target", func(t *testing.T) {
		// Target has a value return, so it must return a usable zero value
		// rather than panic.
		_ = s.Target()
	})
	t.Run("Timeouts", func(t *testing.T) {
		// Timeouts must not panic; a zero policy is returned and callers apply
		// their defaults.
		_ = s.Timeouts().WithDefaults()
	})
}

// The facades are exported struct fields, so reading one through a nil Session
// dereferences the field and cannot be made safe. A nil facade reached any other
// way is the reachable case, and it reports the missing session rather than
// panicking.
func TestNilFacadesReportNilSession(t *testing.T) {
	ctx := context.Background()

	var screen ScreenBundle
	if _, err := screen.Grab(ctx, image.Rectangle{}); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Screen.Grab() = %v, want ErrNilSession", err)
	}

	var input InputBundle
	if err := input.Type(ctx, "x"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Input.Type() = %v, want ErrNilSession", err)
	}

	var windows WindowBundle
	if _, err := windows.List(ctx, window.Match{}); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Windows.List() = %v, want ErrNilSession", err)
	}

	var clipboard *clipboardState
	if _, err := clipboard.Get(ctx); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Clipboard.Get() = %v, want ErrNilSession", err)
	}

	var outputs *outputState
	if _, err := outputs.List(ctx); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Outputs.List() = %v, want ErrNilSession", err)
	}
}

type cancelingScreenDiagnostics struct {
	capabilityScreen
	cancel context.CancelFunc
}

func (b *cancelingScreenDiagnostics) Diagnostics() []string {
	b.cancel()
	return nil
}

type cancelingAccessibilityDiagnostics struct {
	*bundleAccessibilityFake
	cancel context.CancelFunc
	closes atomic.Int32
}

func (b *cancelingAccessibilityDiagnostics) Diagnostics() []string {
	b.cancel()
	return nil
}

func (b *cancelingAccessibilityDiagnostics) Close() error {
	b.closes.Add(1)
	return nil
}

// Cancellation while collecting backend metadata must not publish a session,
// whether further capabilities remain or the last capability has just opened.
func TestOpenCancellationDuringCapabilityDiagnosticsClosesBackend(t *testing.T) {
	t.Run("before next capability", func(t *testing.T) {
		preserveOpeners(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		backend := &cancelingScreenDiagnostics{cancel: cancel}
		openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
			return backend, nil
		}
		session, err := openSession(ctx, Require(CapabilityScreen))
		if session != nil || !errors.Is(err, context.Canceled) || backend.closeCalls.Load() != 1 {
			t.Fatalf("Open = (%v, %v), backend closes=%d", session, err, backend.closeCalls.Load())
		}
	})
	t.Run("after last capability", func(t *testing.T) {
		previous := openAccessibility
		t.Cleanup(func() { openAccessibility = previous })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		backend := &cancelingAccessibilityDiagnostics{bundleAccessibilityFake: &bundleAccessibilityFake{}, cancel: cancel}
		openAccessibility = func(context.Context, env.Runtime) (accessibility.Backend, error) {
			return backend, nil
		}
		session, err := openSession(ctx, Require(CapabilityAccessibility))
		if session != nil || !errors.Is(err, context.Canceled) || backend.closes.Load() != 1 {
			t.Fatalf("Open = (%v, %v), backend closes=%d", session, err, backend.closes.Load())
		}
	})
}

func TestNestedStartupCanceledDuringStaleCleanupDoesNotStartRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	managedStarted := false
	session, err := openSession(ctx,
		WithNested(SessionConfig{Timeouts: TimeoutPolicy{Startup: time.Second}}),
		WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
			if phase == StartupPhaseStaleSessionCleanup && state == StartupPhaseStarted {
				cancel()
			}
			if phase == StartupPhaseManagedRuntimeSetup {
				managedStarted = true
			}
		}),
	)
	if session != nil || !errors.Is(err, context.Canceled) || managedStarted {
		t.Fatalf("nested startup = (%v, %v), managed runtime started=%v", session, err, managedStarted)
	}
}

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
		if socketErr := validateManagedSessionSocketPath(filepath.Join(runtimeDir, socket)); socketErr != nil {
			t.Fatalf("runtime still too long: %v", socketErr)
		}
	}
	info, err := os.Stat(runtimeDir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatalf("runtime dir should be private 0700: %v %v", info, err)
	}
	if !sessionruntime.IsManagedSessionDir(runtimeDir) || !sessionruntime.IsSafeToRemoveDir(runtimeDir) {
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
	if mkdirErr := os.MkdirAll(preferred, 0700); mkdirErr != nil {
		t.Fatal(mkdirErr)
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

type capabilityScreen struct {
	closeCalls atomic.Int32
}

type operationReportingScreen struct {
	capabilityScreen
}

type timeoutReportingScreen struct {
	capabilityScreen
	timeout time.Duration
}

type contextReportingScreen struct {
	capabilityScreen
	contexts chan context.Context
}

func (s *contextReportingScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.contexts <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

type contextReportingAccessibility struct {
	*bundleAccessibilityFake
	contexts chan context.Context
}

func (b *contextReportingAccessibility) Applications(ctx context.Context) ([]accessibility.Application, error) {
	b.contexts <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func (*operationReportingScreen) SupportedOperations() []string {
	return []string{"hash"}
}

func (*operationReportingScreen) Diagnostics() []string {
	return []string{"capture cost: test backend", "connection health: healthy"}
}

func (s *timeoutReportingScreen) SetCaptureTimeout(timeout time.Duration) {
	s.timeout = timeout
}

func (s *capabilityScreen) Grab(
	context.Context,
	image.Rectangle,
) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}

func (s *capabilityScreen) GrabFullHash(context.Context) (uint32, error) {
	return 1, nil
}

func (s *capabilityScreen) GrabRegionHash(
	context.Context,
	image.Rectangle,
) (uint32, error) {
	return 1, nil
}

func (s *capabilityScreen) Close() error {
	s.closeCalls.Add(1)
	return nil
}

type capabilityClipboard struct{}

func (*capabilityClipboard) Get(context.Context) (string, error) {
	return "", nil
}

func (*capabilityClipboard) Set(context.Context, string) error {
	return nil
}

func (*capabilityClipboard) Close() error {
	return nil
}

func preserveOpeners(t *testing.T) {
	t.Helper()
	savedScreen := openScreen
	savedInput := openInput
	savedWindow := openWindow
	savedOutput := openOutput
	savedClipboard := openClipboard
	t.Cleanup(func() {
		openScreen = savedScreen
		openInput = savedInput
		openWindow = savedWindow
		openOutput = savedOutput
		openClipboard = savedClipboard
	})
}

func TestOpenLeavesUnrequestedCapabilitiesClosed(t *testing.T) {
	preserveOpeners(t)
	var calls atomic.Int32
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		calls.Add(1)
		return nil, errors.New("unexpected screen open")
	}
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) {
		calls.Add(1)
		return nil, errors.New("unexpected input open")
	}
	openWindow = func(context.Context, env.Runtime) (window.Manager, error) {
		calls.Add(1)
		return nil, errors.New("unexpected window open")
	}
	openOutput = func(context.Context, env.Runtime) (output.Lister, error) {
		calls.Add(1)
		return nil, errors.New("unexpected output open")
	}
	openClipboard = func(context.Context, env.Runtime) (clipboard.Clipboard, error) {
		calls.Add(1)
		return nil, errors.New("unexpected clipboard open")
	}

	session, err := openSession(context.Background(), WithLogger(nil))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})
	if calls.Load() != 0 {
		t.Fatalf("backend open calls = %d, want 0", calls.Load())
	}
	for _, status := range session.Capabilities() {
		if status.Requested || status.Available || status.Failure != nil {
			t.Fatalf("unrequested status = %+v", status)
		}
	}
	if session.screen == nil ||
		session.input == nil ||
		session.windows == nil ||
		session.outputs == nil ||
		session.clipboard == nil {
		t.Fatal("Open exposed a nil capability facade")
	}
}

func TestOpenCapabilityInputUsesManagedResolution(t *testing.T) {
	preserveOpeners(t)
	wantErr := errors.New("input opener called")
	var gotX, gotY int32
	openInput = func(_ context.Context, _ env.Runtime, maxX, maxY int32) (input.Inputter, error) {
		gotX, gotY = maxX, maxY
		return nil, wantErr
	}
	s := &sessionCore{
		config: SessionConfig{Resolution: image.Pt(1234, 567)},
		env:    env.FromEnviron(nil),
	}

	_, err := s.openCapability(CapabilityInput)
	if !errors.Is(err, wantErr) {
		t.Fatalf("openCapability error = %v, want %v", err, wantErr)
	}
	if gotX != 1234 || gotY != 567 {
		t.Fatalf("input dimensions = %dx%d, want 1234x567", gotX, gotY)
	}
}

func TestOpenCapabilityScreenUsesEffectiveTimeoutPolicy(t *testing.T) {
	preserveOpeners(t)
	backend := &timeoutReportingScreen{}
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return backend, nil
	}
	s := &sessionCore{
		config: SessionConfig{Timeouts: TimeoutPolicy{Medium: 17 * time.Second}},
		env:    env.FromEnviron(nil),
		screen: &screenState{},
	}

	if _, err := s.openCapability(CapabilityScreen); err != nil {
		t.Fatalf("openCapability screen: %v", err)
	}
	if backend.timeout != 17*time.Second {
		t.Fatalf("capture timeout = %s, want 17s", backend.timeout)
	}
}

func TestOpenCapabilityPassesEffectiveDeadlineToBackendSetup(t *testing.T) {
	preserveOpeners(t)
	backend := &capabilityScreen{}
	observed := make(chan time.Time, 1)
	openScreen = func(ctx context.Context, _ env.Runtime) (screen.Screenshotter, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Fatal("capability opener context has no deadline")
		}
		observed <- deadline
		return backend, nil
	}
	s := &sessionCore{
		config: SessionConfig{Timeouts: TimeoutPolicy{Startup: 40 * time.Millisecond}},
		env:    env.FromEnviron(nil),
		screen: &screenState{bundleBase: bundleBase{session: nil, capability: CapabilityScreen}},
	}
	s.screen.session = s
	if _, err := s.openCapability(CapabilityScreen); err != nil {
		t.Fatalf("openCapability screen: %v", err)
	}
	deadline := <-observed
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 40*time.Millisecond {
		t.Fatalf("opener deadline remaining = %s, want <= 40ms and positive", remaining)
	}
}

func TestScreenCaptureUsesEffectiveTimeoutAndReturnsDeadlineCause(t *testing.T) {
	backend := &contextReportingScreen{contexts: make(chan context.Context, 1)}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	session.config.Timeouts = TimeoutPolicy{Medium: 20 * time.Millisecond}
	t.Cleanup(func() { _ = session.Close() })

	result := make(chan error, 1)
	go func() {
		_, err := session.screen.Grab(context.Background(), image.Rect(0, 0, 1, 1))
		result <- err
	}()
	ctx := <-backend.contexts
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("capture context has no effective deadline")
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capture error = %v, want context deadline", err)
	}
}

func TestScreenCaptureCallerDeadlineCapsPolicyDeadline(t *testing.T) {
	backend := &contextReportingScreen{contexts: make(chan context.Context, 1)}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	session.config.Timeouts = TimeoutPolicy{Medium: time.Minute}
	t.Cleanup(func() { _ = session.Close() })

	callerCtx, callerCancel := context.WithTimeout(context.Background(), time.Second)
	defer callerCancel()
	result := make(chan error, 1)
	go func() {
		_, err := session.screen.Grab(callerCtx, image.Rect(0, 0, 1, 1))
		result <- err
	}()

	operationCtx := <-backend.contexts
	wantDeadline, _ := callerCtx.Deadline()
	gotDeadline, ok := operationCtx.Deadline()
	if !ok || !gotDeadline.Equal(wantDeadline) {
		t.Fatalf("capture deadline = %v, present=%v, want caller deadline %v", gotDeadline, ok, wantDeadline)
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("capture error = %v, want caller deadline", err)
	}
}

func TestScreenCapturePreservesCallerCancellationWithPolicy(t *testing.T) {
	backend := &contextReportingScreen{contexts: make(chan context.Context, 1)}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	session.config.Timeouts = TimeoutPolicy{Medium: time.Minute}
	t.Cleanup(func() { _ = session.Close() })

	callerCtx, callerCancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := session.screen.Grab(callerCtx, image.Rect(0, 0, 1, 1))
		result <- err
	}()
	operationCtx := <-backend.contexts
	if _, ok := operationCtx.Deadline(); !ok {
		t.Fatal("capture context has no policy deadline")
	}
	callerCancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("capture error = %v, want caller cancellation", err)
	}
}

func TestAccessibilityUsesEffectiveLongTimeout(t *testing.T) {
	backend := &contextReportingAccessibility{
		bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1},
		contexts:                make(chan context.Context, 1),
	}
	session := newSessionForTesting(nil, nil, nil, nil, nil, backend)
	session.config.Timeouts = TimeoutPolicy{Long: 20 * time.Millisecond}
	t.Cleanup(func() { _ = session.Close() })

	result := make(chan error, 1)
	go func() {
		_, err := session.accessibility.Applications(context.Background())
		result <- err
	}()
	ctx := <-backend.contexts
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("accessibility context has no effective deadline")
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("accessibility error = %v, want context deadline", err)
	}
}

func TestOpenRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := openSession(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", err)
	}
}

func TestOpenStopsCapabilityInitializationWhenContextCanceled(t *testing.T) {
	preserveOpeners(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var inputOpenCalls atomic.Int32
	screenBackend := &capabilityScreen{}
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		cancel()
		return screenBackend, nil
	}
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) {
		inputOpenCalls.Add(1)
		return nil, errors.New("input opener ran after cancellation")
	}

	if session, err := openSession(ctx, Require(CapabilityScreen, CapabilityInput)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", err)
	} else if session != nil {
		t.Fatal("Open returned a session after cancellation")
	}
	if got := inputOpenCalls.Load(); got != 0 {
		t.Fatalf("input opener calls = %d, want 0 after cancellation", got)
	}
	if got := screenBackend.closeCalls.Load(); got != 1 {
		t.Fatalf("opened screen backend close calls = %d, want 1", got)
	}
}

func TestOpenPassesContextToCapabilityOpener(t *testing.T) {
	preserveOpeners(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	openInputCalls := 0
	openScreen = func(ctx context.Context, _ env.Runtime) (screen.Screenshotter, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) {
		openInputCalls++
		return nil, errors.New("input opener ran after cancellation")
	}

	result := make(chan error, 1)
	go func() {
		_, err := openSession(ctx, Require(CapabilityScreen, CapabilityInput))
		result <- err
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", err)
	}
	if openInputCalls != 0 {
		t.Fatalf("input opener calls = %d, want 0 after cancellation", openInputCalls)
	}
}

func TestOpenRequiredAndOptionalCapabilities(t *testing.T) {
	preserveOpeners(t)
	clipboardBackend := &capabilityClipboard{}
	optionalErr := errors.New("outputs unavailable")
	openClipboard = func(context.Context, env.Runtime) (clipboard.Clipboard, error) {
		return clipboardBackend, nil
	}
	openOutput = func(context.Context, env.Runtime) (output.Lister, error) {
		return nil, optionalErr
	}

	session, err := openSession(
		context.Background(),
		Require(CapabilityClipboard),
		Optional(CapabilityOutputs),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})
	if !session.Has(CapabilityClipboard) {
		t.Fatal("required clipboard is unavailable")
	}
	clipboardStatus := session.Capability(CapabilityClipboard)
	if !clipboardStatus.Required ||
		!clipboardStatus.Available ||
		clipboardStatus.Backend == "" ||
		!clipboardStatus.Supports("set") {
		t.Fatalf("clipboard status = %+v", clipboardStatus)
	}
	outputStatus := session.Capability(CapabilityOutputs)
	if outputStatus.Available ||
		outputStatus.Required ||
		!errors.Is(outputStatus.Failure, optionalErr) {
		t.Fatalf("output status = %+v", outputStatus)
	}
	_, err = session.outputs.List(context.Background())
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Capability != CapabilityOutputs ||
		!errors.Is(err, optionalErr) {
		t.Fatalf("Outputs.List error = %v", err)
	}
}

func TestOpenRequiredFailureCleansPartialCapabilities(t *testing.T) {
	preserveOpeners(t)
	screenBackend := &capabilityScreen{}
	inputErr := errors.New("input unavailable")
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return screenBackend, nil
	}
	openInput = func(context.Context, env.Runtime, int32, int32) (input.Inputter, error) {
		return nil, inputErr
	}

	session, err := openSession(
		context.Background(),
		Require(CapabilityScreen, CapabilityInput),
	)
	if session != nil {
		t.Fatal("Open returned a Session after required failure")
	}
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Capability != CapabilityInput ||
		!errors.Is(err, inputErr) {
		t.Fatalf("Open error = %v", err)
	}
	if screenBackend.closeCalls.Load() != 1 {
		t.Fatalf(
			"screen close calls = %d, want 1",
			screenBackend.closeCalls.Load(),
		)
	}
}

func TestOpenTreatsNilBackendAsUnavailable(t *testing.T) {
	preserveOpeners(t)
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return nil, nil
	}

	session, err := openSession(context.Background(), Require(CapabilityScreen))
	if session != nil {
		t.Fatal("Open returned a session for a nil backend")
	}
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Capability != CapabilityScreen ||
		!errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open error = %v, want screen unavailable capability error", err)
	}
}

func TestOpenTreatsTypedNilBackendAsUnavailable(t *testing.T) {
	preserveOpeners(t)
	var backend *capabilityScreen
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return backend, nil
	}

	session, err := openSession(context.Background(), Require(CapabilityScreen))
	if session != nil {
		t.Fatal("Open returned a session for a typed nil backend")
	}
	var capabilityErr *CapabilityError
	if !errors.As(err, &capabilityErr) ||
		capabilityErr.Capability != CapabilityScreen ||
		!errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open error = %v, want screen unavailable capability error", err)
	}
}

func TestNewSessionForTestingTreatsTypedNilBackendAsUnavailable(t *testing.T) {
	var backend *capabilityScreen
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	if session.Has(CapabilityScreen) {
		t.Fatal("typed-nil screen backend was reported as available")
	}
	_, err := session.screen.Grab(context.Background(), image.Rect(0, 0, 1, 1))
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("typed-nil screen operation error = %v, want ErrUnavailable", err)
	}
}

func TestSessionEnforcesBackendOperationReport(t *testing.T) {
	session := newSessionForTesting(
		&operationReportingScreen{},
		nil,
		nil,
		nil,
		nil,
	)
	t.Cleanup(func() { _ = session.Close() })

	status := session.Capability(CapabilityScreen)
	if !status.Available || status.Supports("capture") {
		t.Fatalf("screen status = %+v, want available without capture", status)
	}
	if len(status.Diagnostics) != 2 || status.Diagnostics[0] != "capture cost: test backend" {
		t.Fatalf("screen diagnostics = %v, want backend diagnostics", status.Diagnostics)
	}
	_, err := session.screen.Grab(context.Background(), image.Rect(0, 0, 1, 1))
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unadvertised capture error = %v, want ErrUnsupported", err)
	}
}

func TestTargetOptionsAreMutuallyExclusive(t *testing.T) {
	_, err := openSession(
		context.Background(),
		WithTarget(EnvironmentTarget([]string{})),
		WithHeadless(SessionConfig{}),
	)
	if err == nil {
		t.Fatal("Open accepted multiple target options")
	}
}

func TestLaunchRoutesEnvironmentAndIgnoresLaterContextCancellation(
	t *testing.T,
) {
	session, err := openSession(
		context.Background(),
		WithTarget(EnvironmentTarget([]string{
			"WAYLAND_DISPLAY=target-wayland",
			"DISPLAY=",
		})),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
	})

	var stdout bytes.Buffer
	launchCtx, cancel := context.WithCancel(context.Background())
	app, err := session.Launch(
		launchCtx,
		Command{
			Name: "sh",
			Args: []string{
				"-c",
				`printf '%s|%s' "$CUSTOM" "$WAYLAND_DISPLAY"; sleep 0.02`,
			},
			Env: []string{
				"CUSTOM=caller",
				"WAYLAND_DISPLAY=wrong",
			},
			Stdout: &stdout,
		},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if app.Exited() {
		t.Fatal("application reported exited immediately after launch")
	}
	cancel()
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("Wait after launch cancellation: %v", err)
	}
	if !app.Exited() {
		t.Fatal("application did not report exited after Wait")
	}
	if got := stdout.String(); got != "caller|target-wayland" {
		t.Fatalf("stdout = %q", got)
	}
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("repeated Wait: %v", err)
	}
}

func TestLaunchDoesNotReintroduceHostAccessibilityBus(t *testing.T) {
	t.Setenv("AT_SPI_BUS", "unix:path=/host/accessibility-bus")
	t.Setenv("ATSPI_BUS_ADDRESS", "unix:path=/host/canonical-accessibility-bus")
	t.Setenv("AT_SPI_BUS_ADDRESS", "unix:path=/host/legacy-accessibility-bus")
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	session.env = env.FromEnviron([]string{"PATH=" + os.Getenv("PATH")})
	session.target = DesktopTarget{kind: TargetHeadless, env: session.env.EnvList()}
	t.Cleanup(func() { _ = session.Close() })

	var stdout bytes.Buffer
	app, err := session.Launch(
		context.Background(),
		Command{
			Name:   "sh",
			Args:   []string{"-c", `printf '%s|%s|%s' "${AT_SPI_BUS-unset}" "${ATSPI_BUS_ADDRESS-unset}" "${AT_SPI_BUS_ADDRESS-unset}"`},
			Stdout: &stdout,
		},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := app.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := stdout.String(); got != "unset|unset|unset" {
		t.Fatalf("AT-SPI routing variables in child = %q, want all unset", got)
	}
}

func TestTargetHostPassesHostSessionBusWithoutAccessibilityOverride(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/host/session-bus")
	t.Setenv("ATSPI_BUS_ADDRESS", "")
	previous := openAccessibility
	t.Cleanup(func() { openAccessibility = previous })
	var observed env.Runtime
	openAccessibility = func(_ context.Context, rt env.Runtime) (accessibility.Backend, error) {
		observed = rt
		return &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}, nil
	}

	session, err := openSession(context.Background(), Require(CapabilityAccessibility))
	if err != nil {
		t.Fatalf("Open TargetHost accessibility: %v", err)
	}
	defer session.Close()
	if got := observed.Get("DBUS_SESSION_BUS_ADDRESS"); got != "unix:path=/host/session-bus" {
		t.Fatalf("TargetHost session bus = %q, want host address", got)
	}
	if got := observed.Get("ATSPI_BUS_ADDRESS"); got != "" {
		t.Fatalf("TargetHost unexpectedly supplied ATSPI_BUS_ADDRESS %q", got)
	}
}

func TestSessionCloseStopsApplicationsInReverseLaunchOrder(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	// This asserts shutdown order, and the grace period is only the ceiling
	// before Close escalates to SIGKILL. It has to be comfortably longer than
	// the scheduler needs to deliver SIGTERM to a child and let its trap run,
	// otherwise a loaded host kills the child before it can record anything.
	// The success path returns as soon as the children exit, so a large ceiling
	// costs nothing.
	session.config.ApplicationGracePeriod = 30 * time.Second
	dir := t.TempDir()
	orderPath := filepath.Join(dir, "order")
	readyPath := filepath.Join(dir, "ready")
	launch := func(name string) {
		t.Helper()
		// Announce readiness only after the trap is installed, so shutdown cannot
		// race the shell's own startup and lose the recorded order.
		script := "trap 'printf " + name + " >> \"$ORDER\"; exit 0' TERM; " +
			"printf '" + name + "\\n' >> \"$READY\"; while :; do sleep 1; done"
		if _, err := session.Launch(
			context.Background(),
			Command{
				Name: "sh",
				Args: []string{"-c", script},
				Env:  []string{"ORDER=" + orderPath, "READY=" + readyPath},
			},
		); err != nil {
			t.Fatalf("Launch %s: %v", name, err)
		}
	}
	launch("a")
	launch("b")

	// Wait for both children to report that they can handle TERM. The children
	// are separate processes, so this observes their state rather than assuming
	// a fixed startup delay is long enough.
	readyDeadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(readyPath)
		lines := 0
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if strings.TrimSpace(line) != "" {
					lines++
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read readiness: %v", err)
		}
		if lines >= 2 {
			break
		}
		if time.Now().After(readyDeadline) {
			t.Fatalf("only %d of 2 applications reported readiness", lines)
		}
		time.Sleep(2 * time.Millisecond)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := os.ReadFile(orderPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "ba" {
		t.Fatalf("shutdown order = %q, want ba", got)
	}
	if _, err := session.Launch(
		context.Background(),
		Command{Name: "true"},
	); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Launch after Close error = %v", err)
	}
}

func TestApplicationStopRejectsNilContext(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	app, err := session.Launch(
		context.Background(),
		Command{Name: "sleep", Args: []string{"10"}},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	var nilCtx context.Context
	if err := app.Stop(nilCtx); err == nil {
		t.Fatal("Stop(nil) returned nil")
	} else if !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("Stop(nil) error = %v, want nil-context error", err)
	}

	if app.Exited() {
		t.Fatal("Stop(nil) terminated the application")
	}
	if err := app.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if err := app.Wait(context.Background()); err == nil {
		t.Fatal("Wait after Kill returned nil")
	}
}

func TestStartupWaitHelpersHonorContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, test := range []struct {
		name string
		wait func(context.Context) error
	}{
		{
			name: "file",
			wait: func(ctx context.Context) error {
				return sessionruntime.WaitForFile(ctx, t.TempDir()+"/missing", 100, time.Second)
			},
		},
		{
			name: "glob",
			wait: func(ctx context.Context) error {
				return sessionruntime.WaitForGlob(ctx, t.TempDir()+"/*", 100, time.Second)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.wait(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("wait error = %v, want context.Canceled", err)
			}
		})
	}
}

type trackedCapabilityBackend struct{ closes int }

func (b *trackedCapabilityBackend) Close() error {
	b.closes++
	return errors.New("synthetic close failure")
}

func TestCapabilityOpenRejectsNilBackendAndCleansFailedPartialBackend(t *testing.T) {
	t.Run("nil result never installed", func(t *testing.T) {
		called := 0
		backend, err := openCapabilityBackend(context.Background(), time.Second, CapabilityScreen,
			func(context.Context) (*trackedCapabilityBackend, error) { return nil, nil },
			func(*trackedCapabilityBackend) { called++ }, func(*trackedCapabilityBackend) { called++ })
		if backend != nil || !errors.Is(err, ErrUnavailable) || called != 0 {
			t.Fatalf("nil result %v, %v, callbacks %d", backend, err, called)
		}
	})
	t.Run("partially opened failed backend is closed once", func(t *testing.T) {
		partial := &trackedCapabilityBackend{}
		cause := errors.New("open transport failure")
		called := 0
		backend, err := openCapabilityBackend(context.Background(), time.Second, CapabilityInput,
			func(context.Context) (*trackedCapabilityBackend, error) { return partial, cause },
			func(*trackedCapabilityBackend) { called++ }, func(*trackedCapabilityBackend) { called++ })
		if backend != partial || !errors.Is(err, cause) || partial.closes != 1 || called != 0 {
			t.Fatalf("failed result %v, %v; closes=%d callbacks=%d", backend, err, partial.closes, called)
		}
	})
	t.Run("successful backend configured installed and retained", func(t *testing.T) {
		successful := &trackedCapabilityBackend{}
		order := ""
		got, err := openCapabilityBackend(context.Background(), time.Second, CapabilityWindows,
			func(context.Context) (*trackedCapabilityBackend, error) { order += "open "; return successful, nil },
			func(*trackedCapabilityBackend) { order += "configure " }, func(*trackedCapabilityBackend) { order += "install" })
		if err != nil || got != successful || order != "open configure install" || successful.closes != 0 {
			t.Fatalf("success got %v err %v order %q closes %d", got, err, order, successful.closes)
		}
	})
}

func TestCapabilityOpenRespectsContextAndStartupTimeout(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name          string
		createContext func() context.Context
		timeout       time.Duration
		want          error
	}{
		{"already canceled", func() context.Context { return canceled }, time.Second, context.Canceled},
		{"expired startup", context.Background, 0, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installed := false
			got, err := openCapabilityBackend(tc.createContext(), tc.timeout, CapabilityClipboard,
				func(ctx context.Context) (*trackedCapabilityBackend, error) { return nil, ctx.Err() }, nil,
				func(*trackedCapabilityBackend) { installed = true })
			if got != nil || !errors.Is(err, tc.want) || installed {
				t.Fatalf("canceled open got %v err %v installed=%t", got, err, installed)
			}
		})
	}
}

func TestCloseFailedBackendIgnoresNonClosableAndTypedNil(t *testing.T) {
	var typedNil *trackedCapabilityBackend
	closeFailedBackend(typedNil, errors.New("failure"))
	closeFailedBackend(struct{}{}, errors.New("failure"))
	complete := &trackedCapabilityBackend{}
	closeFailedBackend(complete, nil)
	if complete.closes != 0 {
		t.Fatal("success backend closed")
	}
	closeFailedBackend(complete, errors.New("failure"))
	if complete.closes != 1 {
		t.Fatalf("failed backend closed %d times", complete.closes)
	}
	if got := nilBackendError(CapabilityScreen); !errors.Is(got, ErrUnavailable) || !strings.Contains(got.Error(), "screen") {
		t.Fatalf("nil backend error %v", got)
	}
}

type startupEvent struct {
	phase StartupPhase
	state StartupPhaseState
}

func TestOpenStartupObserverReportsHealthyHeadlessInitialization(t *testing.T) {
	prepareHeadlessHelpers(t, true)
	preserveOpeners(t)
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return &capabilityScreen{}, nil
	}

	var events []startupEvent
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := openSession(
		ctx,
		WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond}}),
		Require(CapabilityScreen),
		WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
			events = append(events, startupEvent{phase: phase, state: state})
		}),
	)
	if err != nil {
		t.Fatalf("synthetic headless Open: %v", err)
	}
	if session.Target().Kind() != TargetHeadless {
		t.Fatalf("Open target = %q, want headless", session.Target().Kind())
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close synthetic headless session: %v", err)
	}

	for _, want := range []startupEvent{
		{StartupPhaseStaleSessionCleanup, StartupPhaseStarted},
		{StartupPhaseStaleSessionCleanup, StartupPhaseSucceeded},
		{StartupPhaseManagedRuntimeSetup, StartupPhaseStarted},
		{StartupPhaseRuntimeDirectoryPreparation, StartupPhaseSucceeded},
		{StartupPhaseDBusStartup, StartupPhaseSucceeded},
		{StartupPhaseDBusProcessSpawn, StartupPhaseSucceeded},
		{StartupPhaseDBusSocketReady, StartupPhaseSucceeded},
		{StartupPhaseSwayConfiguration, StartupPhaseSucceeded},
		{StartupPhaseSwayStartup, StartupPhaseSucceeded},
		{StartupPhaseSwayProcessSpawn, StartupPhaseSucceeded},
		{StartupPhaseSwaySocketsReady, StartupPhaseSucceeded},
		{StartupPhaseClipboardHelper, StartupPhaseSucceeded},
		{StartupPhaseCapabilityScreen, StartupPhaseSucceeded},
		{StartupPhaseManagedRuntimeSetup, StartupPhaseSucceeded},
	} {
		if !hasStartupEvent(events, want) {
			t.Fatalf("healthy startup omitted %+v from events %+v", want, events)
		}
	}
	for _, event := range events {
		if event.state == StartupPhaseFailed || event.state == StartupPhaseCanceled || event.state == StartupPhaseTimedOut {
			t.Fatalf("healthy startup reported terminal failure: %+v", event)
		}
		if strings.Contains(string(event.phase), string(filepath.Separator)) {
			t.Fatalf("startup phase contains a path: %+v", event)
		}
	}
}

func TestOpenStartupObserverIdentifiesCanceledHeadlessDBusReadiness(t *testing.T) {
	tmp := prepareHeadlessHelpers(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events := make(chan startupEvent, 32)
	result := make(chan error, 1)
	go func() {
		_, err := openSession(
			ctx,
			WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: 30 * time.Second, Poll: time.Millisecond}}),
			WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
				events <- startupEvent{phase: phase, state: state}
			}),
		)
		result <- err
	}()

	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if event == (startupEvent{StartupPhaseDBusSocketReady, StartupPhaseStarted}) {
				cancel()
				goto canceled
			}
		case <-deadline.C:
			cancel()
			t.Fatal("headless startup did not reach the synthetic D-Bus readiness wait")
		}
	}

canceled:
	var openErr error
	select {
	case openErr = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("Open did not return after canceling synthetic D-Bus readiness")
	}
	if !errors.Is(openErr, context.Canceled) {
		t.Fatalf("Open error = %v, want context.Canceled", openErr)
	}
	for {
		select {
		case event := <-events:
			if event == (startupEvent{StartupPhaseDBusSocketReady, StartupPhaseCanceled}) {
				goto checked
			}
		default:
			t.Fatal("D-Bus readiness cancellation result was not reported")
		}
	}

checked:
	for _, phase := range []StartupPhase{
		StartupPhaseSwayProcessSpawn,
		StartupPhaseSwaySocketsReady,
		StartupPhaseCapabilityScreen,
	} {
		if hasStartupPhase(events, phase) {
			t.Fatalf("startup passed canceled D-Bus readiness and entered %q", phase)
		}
	}
	requireNoManagedSessionDirs(t, tmp)
}

func TestOpenStartupObserverIdentifiesTimedOutHeadlessDBusReadiness(t *testing.T) {
	tmp := prepareHeadlessHelpers(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	events := make(chan startupEvent, 32)
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := openSession(
			ctx,
			WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: time.Second, Poll: time.Millisecond}}),
			WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
				events <- startupEvent{phase: phase, state: state}
				if phase == StartupPhaseDBusSocketReady && state == StartupPhaseStarted {
					close(started)
				}
			}),
		)
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("headless startup did not reach the synthetic D-Bus readiness wait")
	}
	var openErr error
	select {
	case openErr = <-result:
	case <-time.After(time.Second):
		t.Fatal("Open did not return after the synthetic startup deadline")
	}
	if !errors.Is(openErr, context.DeadlineExceeded) {
		t.Fatalf("Open error = %v, want context.DeadlineExceeded", openErr)
	}
	observed := drainStartupEvents(events)
	for _, want := range []startupEvent{
		{StartupPhaseDBusSocketReady, StartupPhaseTimedOut},
		{StartupPhaseDBusStartup, StartupPhaseTimedOut},
		{StartupPhaseManagedRuntimeSetup, StartupPhaseTimedOut},
	} {
		if !hasStartupEvent(observed, want) {
			t.Fatalf("timed-out startup omitted %+v from events %+v", want, observed)
		}
	}
	if hasStartupEvent(observed, startupEvent{StartupPhaseSwayProcessSpawn, StartupPhaseStarted}) {
		t.Fatal("startup passed the timed-out D-Bus readiness stage")
	}
	requireNoManagedSessionDirs(t, tmp)
}

func TestOpenStartupObserverCallbackPanicDoesNotBreakOpen(t *testing.T) {
	prepareHeadlessHelpers(t, true)
	preserveOpeners(t)
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return &capabilityScreen{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := openSession(
		ctx,
		WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{Startup: 2 * time.Second, Poll: time.Millisecond}}),
		Require(CapabilityScreen),
		WithStartupPhaseObserver(func(StartupPhase, StartupPhaseState) {
			panic("synthetic observer panic")
		}),
	)
	if err != nil {
		t.Fatalf("observer panic changed Open result: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close synthetic headless session: %v", err)
	}
}

func TestOpenStartupObserverReportsOptionalAccessibilityAddressTimeout(t *testing.T) {
	tmp := prepareHeadlessHelpers(t, true)
	preserveOpeners(t)
	savedAccessibility := openAccessibility
	t.Cleanup(func() { openAccessibility = savedAccessibility })
	openScreen = func(context.Context, env.Runtime) (screen.Screenshotter, error) {
		return &capabilityScreen{}, nil
	}
	openAccessibility = func(context.Context, env.Runtime) (accessibility.Backend, error) {
		return &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}, nil
	}
	launcher := filepath.Join(tmp, "bin", atspiBusLauncherName)
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatalf("write synthetic AT-SPI launcher: %v", err)
	}
	var events []startupEvent
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := openSession(
		ctx,
		WithHeadless(SessionConfig{Timeouts: TimeoutPolicy{
			Startup: time.Second,
			Medium:  30 * time.Millisecond,
			Poll:    time.Millisecond,
		}}),
		Require(CapabilityScreen),
		Optional(CapabilityAccessibility),
		WithStartupPhaseObserver(func(phase StartupPhase, state StartupPhaseState) {
			events = append(events, startupEvent{phase: phase, state: state})
		}),
	)
	if err != nil {
		t.Fatalf("Open with optional synthetic accessibility: %v", err)
	}
	if !session.Has(CapabilityScreen) || !session.Has(CapabilityAccessibility) {
		t.Fatalf("optional accessibility address timeout changed capability availability: screen=%v accessibility=%v", session.Has(CapabilityScreen), session.Has(CapabilityAccessibility))
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close synthetic headless session: %v", err)
	}
	for _, want := range []startupEvent{
		{StartupPhaseAccessibilityLauncher, StartupPhaseSucceeded},
		{StartupPhaseAccessibilityBusAddress, StartupPhaseStarted},
		{StartupPhaseAccessibilityBusAddress, StartupPhaseTimedOut},
		{StartupPhaseSwaySocketsReady, StartupPhaseSucceeded},
		{StartupPhaseCapabilityAccessibility, StartupPhaseSucceeded},
	} {
		if !hasStartupEvent(events, want) {
			t.Fatalf("optional accessibility replay omitted %+v from events %+v", want, events)
		}
	}
}

func prepareHeadlessHelpers(t *testing.T, ready bool) string {
	t.Helper()
	tmp, err := os.MkdirTemp("", "pf-open-")
	if err != nil {
		t.Fatal(err)
	}
	alias, err := os.MkdirTemp("/tmp", "pfo-")
	if err != nil {
		_ = os.RemoveAll(tmp)
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		_ = os.RemoveAll(tmp)
		t.Fatal(err)
	}
	if err := os.Symlink(tmp, alias); err != nil {
		_ = os.RemoveAll(tmp)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(alias)
		_ = os.RemoveAll(tmp)
	})
	bin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", alias)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dbusBody := "exec /bin/sleep 30"
	swayBody := "exec /bin/sleep 30"
	if ready {
		// Synthetic helpers must create real UNIX sockets: production startup
		// correctly rejects files and symlinks masquerading as IPC readiness.
		dbusBody = "exec python3 -c 'import os,socket,time; s=socket.socket(socket.AF_UNIX); s.bind(os.environ[\"XDG_RUNTIME_DIR\"] + \"/bus\"); s.listen(1); time.sleep(30)'"
		swayBody = "exec python3 -c 'import os,socket,time; a=socket.socket(socket.AF_UNIX); b=socket.socket(socket.AF_UNIX); a.bind(os.environ[\"XDG_RUNTIME_DIR\"] + \"/wayland-1\"); a.listen(1); b.bind(os.environ[\"XDG_RUNTIME_DIR\"] + \"/sway-ipc.synthetic.sock\"); b.listen(1); time.sleep(30)'"

	}
	for name, body := range map[string]string{
		"dbus-daemon": dbusBody,
		"sway":        swayBody,
		"wl-paste":    "exec /bin/sleep 30",
	} {
		path := filepath.Join(bin, name)
		contents := fmt.Sprintf("#!/bin/sh\nset -eu\n%s\n", body)
		if err := os.WriteFile(path, []byte(contents), 0700); err != nil {
			t.Fatalf("write synthetic %s helper: %v", name, err)
		}
	}
	return tmp
}

func hasStartupEvent(events []startupEvent, want startupEvent) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func hasStartupPhase(events <-chan startupEvent, phase StartupPhase) bool {
	for {
		select {
		case event := <-events:
			if event.phase == phase {
				return true
			}
		default:
			return false
		}
	}
}

func drainStartupEvents(events <-chan startupEvent) []startupEvent {
	var drained []startupEvent
	for {
		select {
		case event := <-events:
			drained = append(drained, event)
		default:
			return drained
		}
	}
}

// requireNoManagedSessionDirs verifies failed managed startup never leaks its
// runtime directory, regardless of which stage failed.
func requireNoManagedSessionDirs(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read synthetic temp root: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "perfuncted-xdg-") {
			t.Fatalf("failed startup left managed runtime directory %q", entry.Name())
		}
	}
}
