//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/v2/internal/probe"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func BenchmarkParseKeySend_Literal(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("hello world this is a test string")
	}
}

func BenchmarkParseKeySend_SingleKey(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("{enter}")
	}
}

func BenchmarkParseKeySend_Combo(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("{ctrl+shift+t}")
	}
}

func BenchmarkParseKeySend_Mixed(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("Hello{enter}{ctrl+s}World{escape}")
	}
}

func BenchmarkParseKeySend_HoldRelease(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		_, _ = parseKeySend("{ctrl down}v{ctrl up}")
	}
}

// waylandRuntime returns an env.Runtime with WAYLAND_DISPLAY set.
func waylandRuntime(sock string) env.Runtime {
	return env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + sock,
		"XDG_SESSION_TYPE=wayland",
	})
}

// x11Runtime returns an env.Runtime with DISPLAY set.
func x11Runtime(display string) env.Runtime {
	return env.FromEnviron([]string{
		"DISPLAY=" + display,
	})
}

// emptyRuntime returns an env.Runtime with no display variables.
func emptyRuntime() env.Runtime {
	return env.FromEnviron([]string{})
}

func TestOpenRuntime_EmptyEnv(t *testing.T) {
	// No Wayland socket, no DISPLAY → may still succeed via uinput fallback.
	// Just verify it doesn't panic.
	rt := emptyRuntime()
	_, err := OpenRuntime(rt, 1024, 768)
	if err != nil {
		t.Logf("OpenRuntime returned error (expected in envs without /dev/uinput): %v", err)
	} else {
		t.Log("OpenRuntime succeeded via uinput fallback")
	}
}

func TestOpenRuntime_UnreachableWayland(t *testing.T) {
	// Unreachable Wayland socket → falls through to uinput if available.
	rt := waylandRuntime("/nonexistent.sock")
	_, err := OpenRuntime(rt, 1024, 768)
	if err != nil {
		t.Logf("OpenRuntime returned error (expected without /dev/uinput): %v", err)
	} else {
		t.Log("OpenRuntime succeeded via uinput fallback")
	}
}

func TestOpenRuntime_ForceUinput(t *testing.T) {
	t.Setenv("PF_FORCE_INPUT", "uinput")
	defer t.Setenv("PF_FORCE_INPUT", "")

	rt := waylandRuntime("wayland-0")
	_, err := OpenRuntime(rt, 1024, 768)
	// May or may not succeed depending on /dev/uinput access.
	// Just verify no panic.
	if err != nil {
		t.Logf("OpenRuntime returned expected error: %v", err)
	}
}

func TestProbeRuntime_Wayland(t *testing.T) {
	rt := waylandRuntime("/nonexistent.sock")
	results := ProbeRuntime(rt)

	// Should have 2 results: wl-virtual, uinput.
	if len(results) != 2 {
		t.Fatalf("expected 2 probe results, got %d", len(results))
	}
	if results[0].Name != "wl-virtual" {
		t.Errorf("first = %q, want wl-virtual", results[0].Name)
	}
	if results[1].Name != "uinput" {
		t.Errorf("second = %q, want uinput", results[1].Name)
	}
	// All should be unavailable (socket unreachable, /dev/uinput likely absent)
	for _, r := range results {
		if r.Name == "" {
			t.Error("probe result missing name")
		}
		if r.Reason == "" {
			t.Errorf("result %q missing reason", r.Name)
		}
	}
}

func TestProbeRuntime_X11(t *testing.T) {
	rt := x11Runtime(":99")
	results := ProbeRuntime(rt)

	// With no Wayland, should have xtest + uinput results
	if len(results) < 2 {
		t.Fatalf("expected at least 2 probe results, got %d", len(results))
	}
	if results[0].Name != "xtest" {
		t.Errorf("first = %q, want xtest", results[0].Name)
	}
}

func TestProbeRuntime_Empty(t *testing.T) {
	rt := emptyRuntime()
	results := ProbeRuntime(rt)

	// Should still return populated results
	if len(results) < 1 {
		t.Fatal("expected at least 1 probe result")
	}
	// Verify all results have names and reasons
	for _, r := range results {
		if r.Name == "" {
			t.Error("probe result missing name")
		}
		if r.Reason == "" {
			t.Errorf("result %q missing reason", r.Name)
		}
	}
}

func TestProbeSelectBest_FirstWins(t *testing.T) {
	results := probe.SelectBest([]probe.Result{
		{Name: "first", Available: false, Reason: "no"},
		{Name: "second", Available: true, Reason: "yes"},
		{Name: "third", Available: true, Reason: "yes"},
	})
	if results[0].Selected {
		t.Error("first should not be selected")
	}
	if !results[1].Selected {
		t.Error("second should be selected")
	}
	if results[2].Selected {
		t.Error("third should not be selected (second already selected)")
	}
}

func TestProbeSelectBest_NoneAvailable(t *testing.T) {
	results := probe.SelectBest([]probe.Result{
		{Name: "a", Available: false},
		{Name: "b", Available: false},
	})
	for _, r := range results {
		if r.Selected {
			t.Errorf("%s should not be selected", r.Name)
		}
	}
}

func TestCheckXTest_NoDisplay(t *testing.T) {
	rt := emptyRuntime()
	r := checkXTest(rt)
	if r.Available {
		t.Error("xtest should not be available without DISPLAY")
	}
	if r.Name != "xtest" {
		t.Errorf("name = %q, want xtest", r.Name)
	}
	if r.Reason != "DISPLAY not set" {
		t.Errorf("reason = %q, want %q", r.Reason, "DISPLAY not set")
	}
}

func TestInputRuntimeStopsBackendSelectionWhenCompositorProbeIsCanceled(t *testing.T) {
	resetRuntimeSelectorConstructors(t)
	fallbackCalls := 0
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		fallbackCalls++
		return nil, errors.New("unexpected virtual backend")
	}
	statUinput = func() error {
		fallbackCalls++
		return nil
	}
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connected := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		peer, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		defer peer.Close()
		_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
		var requests [24]byte
		if _, err := io.ReadFull(peer, requests[:]); err != nil {
			serverDone <- err
			return
		}
		close(connected)
		_, err = io.Copy(io.Discard, peer)
		serverDone <- err
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=" + socket, "XDG_CURRENT_DESKTOP=Sway"})
	type result struct {
		backend Inputter
		err     error
	}
	opened := make(chan result, 1)
	go func() {
		backend, err := OpenRuntimeContext(ctx, rt, 1024, 768)
		opened <- result{backend, err}
	}()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("input discovery did not reach compositor registry probe")
	}
	cancel()
	select {
	case got := <-opened:
		if got.backend != nil || !errors.Is(got.err, context.Canceled) || fallbackCalls != 0 {
			t.Fatalf("canceled discovery returned backend=%T err=%v fallback calls=%d", got.backend, got.err, fallbackCalls)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled input discovery did not return")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("compositor probe did not close cleanly: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled input discovery leaked compositor connection")
	}
}

type runtimeGnomeInputBridge struct {
	mu     sync.Mutex
	events []string
}

func (*runtimeGnomeInputBridge) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}

func (*runtimeGnomeInputBridge) GetExtensionVersion() (string, *dbus.Error) {
	return gnomebridge.ExtensionVersion, nil
}

func (*runtimeGnomeInputBridge) GetShellVersion() (string, *dbus.Error) {
	return "51.0", nil
}

func (*runtimeGnomeInputBridge) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityInput}, nil
}

func (b *runtimeGnomeInputBridge) Key(keyval uint32, pressed bool) *dbus.Error {
	b.mu.Lock()
	b.events = append(b.events, fmt.Sprintf("key:%x:%t", keyval, pressed))
	b.mu.Unlock()
	return nil
}

func (b *runtimeGnomeInputBridge) Text(text string) *dbus.Error {
	b.mu.Lock()
	b.events = append(b.events, "text:"+text)
	b.mu.Unlock()
	return nil
}

func TestInputRuntimeSelectsCurrentGnomeNativeBackend(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	service := &runtimeGnomeInputBridge{}
	reply, err := portal.conn.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME bridge name: reply=%v err=%v", reply, err)
	}
	if err := portal.conn.Export(service, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatalf("export GNOME input bridge core: %v", err)
	}
	if err := portal.conn.Export(service, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.InputInterface); err != nil {
		t.Fatalf("export GNOME input bridge operations: %v", err)
	}
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})

	backend, err := OpenRuntimeContext(context.Background(), runtime, 1920, 1080)
	if err != nil {
		t.Fatalf("open GNOME input runtime with current bridge: %v", err)
	}
	native, ok := backend.(*GnomeNativeBackend)
	if !ok {
		_ = backend.Close()
		t.Fatalf("GNOME runtime selected %T, want native input bridge", backend)
	}
	ctx := context.Background()
	if err := native.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatalf("send modifier press through selected bridge: %v", err)
	}
	if err := native.TypeLiteral(ctx, "hello"); err != nil {
		t.Fatalf("send text through selected bridge: %v", err)
	}
	if err := native.KeyUp(ctx, "ctrl"); err != nil {
		t.Fatalf("send modifier release through selected bridge: %v", err)
	}
	service.mu.Lock()
	events := slices.Clone(service.events)
	service.mu.Unlock()
	if want := []string{"key:ffe3:true", "text:hello", "key:ffe3:false"}; !slices.Equal(events, want) {
		t.Fatalf("GNOME bridge events %v, want %v", events, want)
	}
	if err := native.Close(); err != nil {
		t.Fatalf("close GNOME native input backend: %v", err)
	}

	var available, selected bool
	for _, result := range ProbeRuntime(runtime) {
		if result.Name == "gnome-native" {
			available, selected = result.Available, result.Selected
		}
	}
	if !available || !selected {
		t.Fatalf("GNOME capability probe available=%t selected=%t", available, selected)
	}
}

type selectorTrackedInputter struct {
	noopInputter
	closed int
}

func (b *selectorTrackedInputter) Close() error {
	b.closed++
	return nil
}

func resetRuntimeSelectorConstructors(t *testing.T) {
	t.Helper()
	oldGnome, oldVirtual, oldXTest := newGnomeNativeBackend, newWlVirtualBackend, newXTestBackend
	oldUinput, oldStat := newUinputBackend, statUinput
	t.Cleanup(func() {
		newGnomeNativeBackend, newWlVirtualBackend, newXTestBackend = oldGnome, oldVirtual, oldXTest
		newUinputBackend, statUinput = oldUinput, oldStat
	})
	newGnomeNativeBackend = func(ctx context.Context, rt env.Runtime) (Inputter, error) {
		return NewGnomeNativeBackendForRuntimeContext(ctx, rt)
	}
	newWlVirtualBackend = func(ctx context.Context, sock string) (Inputter, error) {
		return NewWlVirtualBackendContext(ctx, sock)
	}
	newXTestBackend = func(display string) (Inputter, error) { return NewXTestBackend(display) }
	newUinputBackend = func(maxX, maxY int32) (Inputter, error) {
		return NewUinputBackend(maxX, maxY)
	}
	statUinput = func() error { _, err := os.Stat("/dev/uinput"); return err }
	t.Setenv("PF_FORCE_INPUT", "")
}

func syntheticGNOMERuntime(t *testing.T) env.Runtime {
	t.Helper()
	return env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
	})
}

func TestOpenRuntimeGNOMESelectionAndFallbackBoundaries(t *testing.T) {
	t.Run("GNOME backend wins", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		want := &selectorTrackedInputter{}
		gnomeCalls, fallbackCalls := 0, 0
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
			gnomeCalls++
			return want, nil
		}
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
			fallbackCalls++
			return nil, errors.New("unexpected fallback")
		}
		got, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
		if err != nil || got != want {
			t.Fatalf("GNOME selection = %T %v", got, err)
		}
		if gnomeCalls != 1 || fallbackCalls != 0 {
			t.Fatalf("constructor calls = GNOME:%d fallback:%d", gnomeCalls, fallbackCalls)
		}
	})

	t.Run("nil GNOME result continues to virtual input", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		gnomeCalls, virtualCalls := 0, 0
		want := &selectorTrackedInputter{}
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
			gnomeCalls++
			return nil, nil
		}
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
			virtualCalls++
			return want, nil
		}
		got, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
		if err != nil || got != want {
			t.Fatalf("virtual fallback = %T %v", got, err)
		}
		if gnomeCalls != 1 || virtualCalls != 1 {
			t.Fatalf("constructor calls = GNOME:%d virtual:%d", gnomeCalls, virtualCalls)
		}
	})

	t.Run("restart-required bridge does not fall back", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		fallbackCalls := 0
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
			return nil, gnomebridge.ErrSessionRestartRequired
		}
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
			fallbackCalls++
			return &selectorTrackedInputter{}, nil
		}
		got, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
		if got != nil || !errors.Is(err, gnomebridge.ErrSessionRestartRequired) {
			t.Fatalf("restart-required selection = %T %v", got, err)
		}
		if fallbackCalls != 0 {
			t.Fatalf("fallback constructors called %d times", fallbackCalls)
		}
	})

	t.Run("bridge and fallback causes remain inspectable", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		bridgeErr := errors.New("synthetic GNOME bridge unavailable")
		virtualErr := errors.New("synthetic virtual protocol unavailable")
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) { return nil, bridgeErr }
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) { return nil, virtualErr }
		statUinput = func() error { return os.ErrPermission }
		_, err := OpenRuntimeContext(context.Background(), syntheticGNOMERuntime(t), 1024, 768)
		if !errors.Is(err, bridgeErr) || !errors.Is(err, virtualErr) {
			t.Fatalf("fallback error lost causes: %v", err)
		}
		if !strings.Contains(err.Error(), "no backend available for Wayland desktop") {
			t.Fatalf("fallback summary missing: %v", err)
		}
	})

	t.Run("canceled bridge closes a partial backend", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		ctx, cancel := context.WithCancel(context.Background())
		want := &selectorTrackedInputter{}
		newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
			cancel()
			return want, errors.New("late bridge failure")
		}
		fallbackCalls := 0
		newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
			fallbackCalls++
			return nil, errors.New("must not run after cancellation")
		}
		got, err := OpenRuntimeContext(ctx, syntheticGNOMERuntime(t), 1024, 768)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled GNOME selection = %T %v", got, err)
		}
		if want.closed != 1 || fallbackCalls != 0 {
			t.Fatalf("partial backend closes=%d fallback calls=%d", want.closed, fallbackCalls)
		}
	})
}

func TestOpenRuntimeContextClosesGNOMEBackendReturnedAfterCancellation(t *testing.T) {
	resetRuntimeSelectorConstructors(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &selectorTrackedInputter{}
	newGnomeNativeBackend = func(context.Context, env.Runtime) (Inputter, error) {
		cancel()
		return backend, nil
	}
	fallbackCalls := 0
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		fallbackCalls++
		return nil, errors.New("fallback must not run after cancellation")
	}

	got, err := OpenRuntimeContext(ctx, syntheticGNOMERuntime(t), 1024, 768)
	if got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GNOME selection = %T %v", got, err)
	}
	if backend.closed != 1 || fallbackCalls != 0 {
		t.Fatalf("late backend closes=%d fallback calls=%d", backend.closed, fallbackCalls)
	}
}

func TestOpenRuntimeForcedUinputBoundariesStayExplicit(t *testing.T) {
	t.Run("inaccessible forced device does not fall back", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		t.Setenv("PF_FORCE_INPUT", "uinput")
		statUinput = func() error { return os.ErrPermission }
		constructorCalls := 0
		newUinputBackend = func(int32, int32) (Inputter, error) {
			constructorCalls++
			return &selectorTrackedInputter{}, nil
		}
		got, err := OpenRuntimeContext(context.Background(), env.FromEnviron([]string{"DISPLAY=:99"}), 1024, 768)
		if got != nil || err == nil || !strings.Contains(err.Error(), "not accessible") {
			t.Fatalf("forced unavailable selection = %T %v", got, err)
		}
		if constructorCalls != 0 {
			t.Fatalf("uinput constructor called %d times", constructorCalls)
		}
	})

	t.Run("nil constructor result is rejected", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		t.Setenv("PF_FORCE_INPUT", "uinput")
		statUinput = func() error { return nil }
		newUinputBackend = func(int32, int32) (Inputter, error) { return nil, nil }
		got, err := OpenRuntimeContext(context.Background(), env.FromEnviron(nil), 1024, 768)
		if got != nil || err == nil || !strings.Contains(err.Error(), "constructor returned nil backend") {
			t.Fatalf("forced nil constructor result = %T %v", got, err)
		}
	})

	t.Run("cancellation closes constructor result", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		t.Setenv("PF_FORCE_INPUT", "uinput")
		statUinput = func() error { return nil }
		ctx, cancel := context.WithCancel(context.Background())
		want := &selectorTrackedInputter{}
		newUinputBackend = func(int32, int32) (Inputter, error) {
			cancel()
			return want, nil
		}
		got, err := OpenRuntimeContext(ctx, env.FromEnviron(nil), 1024, 768)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("forced canceled selection = %T %v", got, err)
		}
		if want.closed != 1 {
			t.Fatalf("partial uinput backend closed %d times", want.closed)
		}
	})

	t.Run("late X11 fallback cancellation closes constructor result", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		statUinput = func() error { return nil }
		newXTestBackend = func(string) (Inputter, error) { return nil, errors.New("XTEST unavailable") }
		ctx, cancel := context.WithCancel(context.Background())
		want := &selectorTrackedInputter{}
		newUinputBackend = func(int32, int32) (Inputter, error) {
			cancel()
			return want, nil
		}
		got, err := OpenRuntimeContext(ctx, env.FromEnviron([]string{"DISPLAY=:99"}), 1024, 768)
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled X11 fallback selection = %T %v", got, err)
		}
		if want.closed != 1 {
			t.Fatalf("partial uinput backend closed %d times", want.closed)
		}
	})
}

func TestOpenRuntimeContextCancellationStopsBackendFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rt           func(*testing.T) env.Runtime
		cancelBefore bool
	}{
		{
			name:         "already canceled",
			rt:           func(*testing.T) env.Runtime { return env.FromEnviron(nil) },
			cancelBefore: true,
		},
		{
			name: "virtual input",
			rt: func(t *testing.T) env.Runtime {
				return env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-missing", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=SWAY"})
			},
		},
		{
			name: "Wayland uinput",
			rt: func(t *testing.T) env.Runtime {
				return env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-missing", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=SWAY"})
			},
		},
		{
			name: "Wayland XTEST",
			rt: func(t *testing.T) env.Runtime {
				return env.FromEnviron([]string{"DISPLAY=:99", "WAYLAND_DISPLAY=wayland-missing", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=SWAY"})
			},
		},
		{
			name: "X11 XTEST",
			rt:   func(*testing.T) env.Runtime { return env.FromEnviron([]string{"DISPLAY=:99"}) },
		},
		{
			name: "missing-device probe",
			rt:   func(*testing.T) env.Runtime { return env.FromEnviron(nil) },
		},
		{
			name: "Wayland final cancellation check",
			rt: func(t *testing.T) env.Runtime {
				return env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-missing", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=SWAY"})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRuntimeSelectorConstructors(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
				if tc.name != "virtual input" {
					return nil, errors.New("synthetic virtual input unavailable")
				}
				cancel()
				return nil, errors.New("virtual input canceled")
			}
			newXTestBackend = func(string) (Inputter, error) {
				if tc.name == "Wayland XTEST" || tc.name == "X11 XTEST" {
					cancel()
				}
				return nil, errors.New("synthetic XTEST unavailable")
			}
			newUinputBackend = func(int32, int32) (Inputter, error) {
				if tc.name == "Wayland uinput" {
					cancel()
					return nil, errors.New("uinput canceled")
				}
				return nil, errors.New("synthetic uinput unavailable")
			}
			statUinput = func() error {
				if tc.name == "Wayland uinput" {
					return nil
				}
				if tc.name == "missing-device probe" || tc.name == "Wayland final cancellation check" {
					cancel()
				}
				return os.ErrPermission
			}
			_, err := OpenRuntimeContext(ctx, tc.rt(t), 1024, 768)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("OpenRuntimeContext error = %v", err)
			}
		})
	}
}

func TestOpenRuntimeRejectsNilFinalUinputBackendAndReportsNoBackend(t *testing.T) {
	t.Run("nil uinput constructor", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		statUinput = func() error { return nil }
		newUinputBackend = func(int32, int32) (Inputter, error) { return nil, nil }
		_, err := OpenRuntimeContext(context.Background(), env.FromEnviron(nil), 1024, 768)
		if err == nil || !strings.Contains(err.Error(), "constructor returned nil backend") {
			t.Fatalf("nil final uinput backend error = %v", err)
		}
	})

	t.Run("no display and no uinput", func(t *testing.T) {
		resetRuntimeSelectorConstructors(t)
		statUinput = func() error { return os.ErrPermission }
		_, err := OpenRuntimeContext(context.Background(), env.FromEnviron(nil), 1024, 768)
		if err == nil || !strings.Contains(err.Error(), "no backend available") {
			t.Fatalf("empty-runtime selection error = %v", err)
		}
	})
}
