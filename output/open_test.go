package output

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/capability"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/randr"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/wl"
	"github.com/nskaggs/perfuncted/internal/x11"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestX11AndWaylandOutputBackendsShareCapabilityContract(t *testing.T) {
	want := capability.Operations("outputs")
	if len(want) == 0 {
		t.Fatal("missing output operations")
	}
	x11 := (*X11Lister)(nil).SupportedOperations()
	wayland := (*WaylandLister)(nil).SupportedOperations()
	if !reflect.DeepEqual(x11, want) || !reflect.DeepEqual(wayland, want) {
		t.Fatalf("backend mismatch x11=%v wayland=%v expected=%v", x11, wayland, want)
	}
	x11[0] = "tampered"
	wayland[0] = "tampered"
	if !reflect.DeepEqual((*X11Lister)(nil).SupportedOperations(), want) || !reflect.DeepEqual((*WaylandLister)(nil).SupportedOperations(), want) {
		t.Fatal("capability registry aliased to returned slice")
	}
}

// A canceled caller must never discover or attach to an output backend,
// regardless of whether an X11 fallback or a Wayland socket is configured.
func TestOpenRuntimeContextCanceledBeforeBackendSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		vars []string
	}{
		{"headless", nil},
		{"x11", []string{"DISPLAY=:999999"}},
		{"wayland", []string{"WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=/nonexistent/perfuncted"}},
		{"wayland with x11 fallback", []string{"DISPLAY=:999999", "WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=/nonexistent/perfuncted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			lister, err := OpenRuntimeContext(ctx, env.FromEnviron(tc.vars))
			if lister != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled selection = %T, %v", lister, err)
			}
		})
	}
}

func TestOpenRuntimeContextMissingWaylandSocketDoesNotReturnPartialLister(t *testing.T) {
	runtimeDir := t.TempDir()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-unavailable", "XDG_RUNTIME_DIR=" + runtimeDir})
	lister, err := OpenRuntimeContext(context.Background(), rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "socket unreachable") {
		t.Fatalf("missing Wayland socket admitted: lister=%T err=%v", lister, err)
	}
}

func TestOpenRuntimeContextX11OnlyConnectionFailureDoesNotReturnPartialLister(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=invalid-display-name"})
	lister, err := OpenRuntimeContext(context.Background(), rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "output/x11: connect") {
		t.Fatalf("invalid X11 display admitted: lister=%T err=%v", lister, err)
	}
}

func TestOutputProbeAndOpenDistinguishRegularFileFromWaylandSocket(t *testing.T) {
	dir := t.TempDir()
	// A regular file with a Wayland socket's filename is not a compositor.
	path := dir + "/wayland-0"
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-0", "XDG_RUNTIME_DIR=" + dir})
	results := ProbeRuntime(rt)
	if len(results) != 1 || results[0].Available || results[0].Selected || results[0].Name != "wayland" {
		t.Fatalf("regular file falsely probed as a compositor: %+v", results)
	}
	lister, err := OpenRuntimeContext(context.Background(), rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "socket unreachable") {
		t.Fatalf("regular file falsely opened as a compositor: %T %v", lister, err)
	}
}

func TestOpenRuntimeContextUnreachableWaylandX11FallbackFailureHasNilLister(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=invalid-display-name",
		"WAYLAND_DISPLAY=wayland-unavailable",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})
	lister, err := OpenRuntimeContext(context.Background(), rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "output/x11: connect") {
		t.Fatalf("failed fallback leaked typed-nil lister: %T, %v", lister, err)
	}
}

func TestOpenRuntime_NoSessionReturnsError(t *testing.T) {
	rt := env.FromEnviron([]string{})

	if _, err := OpenRuntime(rt); err == nil {
		t.Fatal("OpenRuntime succeeded unexpectedly without DISPLAY or Wayland socket")
	} else if !strings.Contains(err.Error(), "no display or Wayland socket available") {
		t.Fatalf("OpenRuntime error = %v, want no-session error", err)
	}
}

func TestOpenRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	lst, err := OpenRuntime(rt)
	if err != nil {
		if !strings.Contains(err.Error(), `output/x11: connect to display ":99"`) {
			t.Fatalf("OpenRuntime error = %v, want X11 connection error", err)
		}
		return
	}
	if _, ok := lst.(*X11Lister); !ok {
		t.Fatalf("OpenRuntime type = %T, want *X11Lister", lst)
	}
}

func TestProbeRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	got := ProbeRuntime(rt)
	if len(got) != 1 {
		t.Fatalf("ProbeRuntime len = %d, want 1", len(got))
	}
	if got[0].Name != "x11" {
		t.Fatalf("ProbeRuntime name = %q, want x11", got[0].Name)
	}
	if !got[0].Selected || !got[0].Available {
		t.Fatalf("ProbeRuntime selected=%v available=%v, want true/true", got[0].Selected, got[0].Available)
	}
	if !strings.Contains(got[0].Reason, "socket missing") {
		t.Fatalf("ProbeRuntime reason = %q, want missing socket fallback", got[0].Reason)
	}
}

func TestProbeRuntime_UnreachableWaylandWithoutX11ReportsUnavailable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	got := ProbeRuntime(rt)
	if len(got) != 1 {
		t.Fatalf("ProbeRuntime len = %d, want 1", len(got))
	}
	if got[0].Name != "wayland" {
		t.Fatalf("ProbeRuntime name = %q, want wayland", got[0].Name)
	}
	if got[0].Available || got[0].Selected {
		t.Fatalf("ProbeRuntime available=%v selected=%v, want false/false", got[0].Available, got[0].Selected)
	}
	if !strings.Contains(got[0].Reason, "socket unreachable") {
		t.Fatalf("ProbeRuntime reason = %q, want unreachable socket message", got[0].Reason)
	}
}

func TestProbeRuntime_NoSessionReportsUnavailable(t *testing.T) {
	rt := env.FromEnviron([]string{})

	got := ProbeRuntime(rt)
	if len(got) != 1 {
		t.Fatalf("ProbeRuntime len = %d, want 1", len(got))
	}
	if got[0].Name != "output" {
		t.Fatalf("ProbeRuntime name = %q, want output", got[0].Name)
	}
	if got[0].Available || got[0].Selected {
		t.Fatalf("ProbeRuntime available=%v selected=%v, want false/false", got[0].Available, got[0].Selected)
	}
	if !strings.Contains(got[0].Reason, "no output source available") {
		t.Fatalf("ProbeRuntime reason = %q, want no-output-source message", got[0].Reason)
	}
}

// Model cancellation at the two distinct publication gates during backend
// selection. No X11 connection should be attempted after either boundary.
type cancelAfterOutputErrChecks struct {
	context.Context //nolint:containedctx // Synthetic context controls the exact cancellation boundary.
	count, cancelAt int
}

func (c *cancelAfterOutputErrChecks) Err() error {
	c.count++
	if c.count >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestOutputSelectionHonorsCancellationBeforeWaylandProbeAndX11Fallback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cancelAt int
	}{
		{"before Wayland probe", 2},
		{"before X11 fallback", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &cancelAfterOutputErrChecks{Context: context.Background(), cancelAt: tc.cancelAt}
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=nonexistent-output-socket",
				"XDG_RUNTIME_DIR=" + t.TempDir(),
				"DISPLAY=invalid-display-name",
			})
			selected, err := OpenRuntimeContext(ctx, rt)
			if selected != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation gate %d returned lister=%T err=%v", tc.cancelAt, selected, err)
			}
			if ctx.count != tc.cancelAt {
				t.Fatalf("checked cancellation %d times, want %d", ctx.count, tc.cancelAt)
			}
		})
	}
}

func TestOutputRuntimeDoesNotFallBackAfterReachableWaylandRejectsDiscovery(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() { done <- serveUnusableWaylandCompositor(listener, nil) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=" + socket, "DISPLAY=invalid-display"})
	lister, err := OpenRuntimeContext(ctx, rt)
	if lister != nil || err == nil || !strings.Contains(err.Error(), "no wl_output globals advertised") {
		if lister != nil {
			_ = lister.Close()
		}
		t.Fatalf("Wayland discovery failure returned lister=%v err=%v", lister, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("failed Wayland discovery did not close its connection")
	}
}

func TestOutputRuntimeReturnsUsableWaylandListerAfterDiscovery(t *testing.T) {
	socket := shortPrivateSocketPath(t)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() { done <- serveWaylandOutputs(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=" + socket, "DISPLAY=invalid-display"})
	lister, err := OpenRuntimeContext(ctx, rt)
	if err != nil || lister == nil {
		t.Fatalf("Wayland discovery returned lister=%v err=%v", lister, err)
	}
	t.Cleanup(func() { _ = lister.Close() })
	outputs, err := lister.List(ctx)
	if err != nil || len(outputs) != 2 || outputs[0].Name != "DP-1" || outputs[1].Name != "DP-2" {
		t.Fatalf("Wayland outputs=%+v err=%v", outputs, err)
	}
	if err := lister.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Wayland lister did not release its connection")
	}
}

func TestX11OutputListRejectsCancellationAfterEmptyRandRQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	primaryQueried := false
	lister := &X11Lister{randrAvailable: true, conn: &x11.MockConnection{
		DefaultScreenFunc: func() *xproto.ScreenInfo {
			return &xproto.ScreenInfo{Root: 42, WidthInPixels: 1920, HeightInPixels: 1080}
		},
		GetScreenResourcesCurrentFunc: func(xproto.Window) x11.RandRScreenResourcesCookie {
			return x11.NewMockRandRScreenResourcesCookie(&randr.GetScreenResourcesCurrentReply{}, nil)
		},
		GetOutputPrimaryFunc: func(xproto.Window) x11.RandROutputPrimaryCookie {
			primaryQueried = true
			cancel()
			return x11.NewMockRandROutputPrimaryCookie(&randr.GetOutputPrimaryReply{}, nil)
		},
	}}
	outputs, err := lister.List(ctx)
	if !primaryQueried || outputs != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled empty RandR query returned outputs=%v err=%v primary queried=%v", outputs, err, primaryQueried)
	}
}

// A reachable Wayland socket takes precedence over an unrelated stale X11
// DISPLAY. A private compositor provides a genuine registry round-trip.
func TestProbeRuntimeSelectsLiveWaylandDespiteX11Fallback(t *testing.T) {
	dir := shortPrivateSocketDir(t)
	socket := filepath.Join(dir, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		var registry uint32
		for {
			request, readErr := readWaylandMessage(conn)
			if readErr != nil {
				done <- readErr
				return
			}
			sender, opcode := wl.Uint32(request[:4]), wl.Uint32(request[4:8])&0xffff
			payload := request[8:]
			if sender != 1 {
				continue
			}
			switch opcode {
			case 1:
				registry = wl.Uint32(payload[:4])
			case 0:
				if err := sendWaylandGlobalEvents(conn, registry); err != nil {
					done <- err
					return
				}
				if err := writeWaylandMessage(conn, wl.Uint32(payload[:4]), 0, nil); err != nil {
					done <- err
					return
				}
				done <- nil
				return
			}
		}
	}()
	rt := env.FromEnviron([]string{"XDG_RUNTIME_DIR=" + dir, "WAYLAND_DISPLAY=wayland.sock", "DISPLAY=invalid-display-name", "XDG_CURRENT_DESKTOP=SWAY"})
	got := ProbeRuntime(rt)
	if len(got) != 1 || got[0].Name != "wayland" || !got[0].Available || !got[0].Selected || got[0].Reason != "wlroots Wayland" {
		t.Fatalf("reachable Wayland probe selected fallback: %+v", got)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("synthetic compositor failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic compositor did not finish registry reply")
	}
}

// A configured pure-X11 session advertises X11 without attempting a network
// connection. Display probing remains usable even when the X server is down.
func TestOutputProbePureX11ReportsSelectedBackendWithoutConnecting(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=invalid-display-name", "WAYLAND_DISPLAY=", "XDG_RUNTIME_DIR="})
	results := ProbeRuntime(rt)
	if len(results) != 1 || results[0].Name != "x11" || !results[0].Available || !results[0].Selected || results[0].Reason != "DISPLAY set" {
		t.Fatalf("pure X11 backend advertisement = %+v, want selected available x11", results)
	}
}
