package screen

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/compositor"
	"github.com/nskaggs/perfuncted/v2/internal/dbusutil"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/gnomebridge"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"github.com/nskaggs/perfuncted/v2/internal/wl"
	"github.com/nskaggs/perfuncted/v2/internal/x11test"
	"go.uber.org/goleak"
	"image"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSyntheticScreenBackendsAdvertiseConsistentCapabilities(t *testing.T) {
	want := capability.Operations("screen")
	backends := []struct {
		name      string
		available func() []string
	}{
		{"x11", func() []string { return (*X11Backend)(nil).SupportedOperations() }},
		{"kwin", func() []string { return (*KWinShotBackend)(nil).SupportedOperations() }},
		{"gnome-shell", func() []string { return (*GnomeShellScreenshotBackend)(nil).SupportedOperations() }},
		{"gnome-native", func() []string { return (*GnomeNativeScreenBackend)(nil).SupportedOperations() }},
		{"external-capture", func() []string { return (*ExtCaptureBackend)(nil).SupportedOperations() }},
		{"wlroots", func() []string { return (*WlrScreencopyBackend)(nil).SupportedOperations() }},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			got := backend.available()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("advertised %v, expected %v", got, want)
			}
			if len(got) == 0 {
				t.Fatal("empty capability contract")
			}
			got[0] = "tampered"
			if !reflect.DeepEqual(backend.available(), want) {
				t.Fatal("client could mutate capability registry")
			}
		})
	}
	portal := (*PortalDBusBackend)(nil)
	expected := capability.Operations("screen", "wait", "wait-change", "wait-stable")
	actual := portal.SupportedOperations()
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("portal advertised %v, expected %v", actual, expected)
	}
	if slices.Contains(actual, "wait-change") || slices.Contains(actual, "wait-stable") {
		t.Fatalf("expensive portal wait advertised: %v", actual)
	}
}

func TestScreenPixelHashImplementationsAdvertiseCanonicalSemantics(t *testing.T) {
	for name, backend := range map[string]interface{ CanonicalHashing() bool }{
		"portal":      (*PortalDBusBackend)(nil),
		"gnome-shell": (*GnomeShellScreenshotBackend)(nil),
		"wlroots":     (*WlrScreencopyBackend)(nil),
	} {
		if !backend.CanonicalHashing() {
			t.Errorf("%s did not advertise pixel hash equivalence", name)
		}
	}
}

func TestSyntheticScreenProbeAdvertisedAndUnavailableProtocols(t *testing.T) {
	for _, tc := range []struct {
		name    string
		globals map[string]bool
		present bool
		why     string
	}{
		{"missing session", nil, false, "no Wayland session"},
		{"empty session", map[string]bool{}, false, "not advertised"},
		{"advertised", map[string]bool{"zwlr_screencopy_manager_v1": true}, true, "advertised"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := checkWlrScreencopy(tc.globals)
			if r.Name != "wlr-screencopy" || r.Available != tc.present || !strings.Contains(r.Reason, tc.why) {
				t.Fatalf("incorrect capability %+v", r)
			}
		})
	}
	for _, tc := range []struct {
		name    string
		globals map[string]bool
		present bool
	}{
		{"not advertised", map[string]bool{}, false},
		{"no session", nil, false},
		{"capture protocol", map[string]bool{"ext_image_copy_capture_manager_v1": true, "ext_output_image_capture_source_manager_v1": true, "wl_shm": true, "wl_output": true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := checkExtCapture(tc.globals)
			if r.Name != "ext-image-copy-capture" || r.Available != tc.present || r.Reason == "" {
				t.Fatalf("incorrect ext-image capability %+v", r)
			}
		})
	}
}
func TestSyntheticScreenProbeDbusFailureBoundaries(t *testing.T) {
	missing := env.FromEnviron(nil)
	unavailable := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-screen-bus"})
	for _, rt := range []env.Runtime{missing, unavailable} {
		r := checkPortalDbus(rt)
		if r.Available || r.Reason != "D-Bus session unavailable" {
			t.Fatalf("unreachable portal %+v", r)
		}
	}
	if r := checkKWinShot(missing, compositor.X11); r.Available || !strings.Contains(r.Reason, "not a KDE") {
		t.Fatalf("X11 KWin %+v", r)
	}
	if r := checkKWinShot(unavailable, compositor.KDE); r.Available || !strings.Contains(r.Reason, "native capture probe failed") {
		t.Fatalf("unavailable KWin %+v", r)
	}
	if r := checkGnomeShellScreenshot(missing, compositor.X11); r.Available || !strings.Contains(r.Reason, "not a GNOME") {
		t.Fatalf("X11 GNOME %+v", r)
	}
	if r := checkGnomeShellScreenshot(unavailable, compositor.GNOME); r.Available || r.Reason == "" {
		t.Fatalf("unavailable GNOME %+v", r)
	}
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestJanitorGoroutineExitsOnClose verifies that the janitor goroutine started
// by withWlrContext is terminated when Close is called.
func TestJanitorGoroutineExitsOnClose(t *testing.T) {
	b := NewWlrScreencopyBackendWithConnector("fake-wl-sock-leak-check",
		func(sock string) (*wl.Context, error) { return &wl.Context{}, nil },
		time.Minute,
	)
	if err := b.withWlrContext(func(ctx *wl.Context) error { return nil }); err != nil {
		t.Fatalf("withWlrContext: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After Close, b.ctx must be nil.
	b.ctxMu.Lock()
	ctxAfterClose := b.ctx
	b.ctxMu.Unlock()
	if ctxAfterClose != nil {
		t.Error("expected b.ctx == nil after Close")
	}
}

func BenchmarkWlrScreencopyBackendOpen(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		be := NewWlrScreencopyBackendWithConnector("bench-fake",
			func(sock string) (*wl.Context, error) { return &wl.Context{}, nil },
			5*time.Minute,
		)
		_ = be.withWlrContext(func(ctx *wl.Context) error { return nil })
		_ = be.Close()
	}
}

func TestWlrScreencopyNilBackendIsSafe(t *testing.T) {
	var backend *WlrScreencopyBackend
	if _, _, err := backend.ResolutionWithContext(context.Background()); err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("ResolutionWithContext error = %v, want nil-backend error", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}

func TestExtCaptureNilBackendIsSafe(t *testing.T) {
	var backend *ExtCaptureBackend
	if _, err := backend.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("Grab error = %v, want nil-backend error", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
}

func TestOpenRuntime_NoSessionReturnsError(t *testing.T) {
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	if _, err := OpenRuntime(rt); err == nil {
		t.Fatal("OpenRuntime succeeded unexpectedly without a reachable backend")
	} else if !strings.Contains(err.Error(), "unsupported Wayland compositor") {
		t.Fatalf("OpenRuntime error = %v, want unsupported-session error", err)
	}
}

func TestOpenRuntimeFallsBackToX11WhenWaylandSocketUnresolvable(t *testing.T) {
	rt := env.FromEnviron([]string{
		"DISPLAY=:99",
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
	})

	scr, err := OpenRuntime(rt)
	if err != nil {
		if !strings.Contains(err.Error(), `screen/x11: connect to display ":99"`) {
			t.Fatalf("OpenRuntime error = %v, want X11 connection error", err)
		}
		return
	}
	if _, ok := scr.(*X11Backend); !ok {
		t.Fatalf("OpenRuntime type = %T, want *X11Backend", scr)
	}
}

func TestProbeRuntime_NoSessionReportsBackends(t *testing.T) {
	rt := env.FromEnviron([]string{})

	got := ProbeRuntime(rt)
	if len(got) != 6 {
		t.Fatalf("ProbeRuntime len = %d, want 6", len(got))
	}
	wantNames := []string{
		"kwin-shot2",
		"gnome-native",
		"wlr-screencopy",
		"ext-image-copy-capture",
		"gnome-shell-screenshot",
		"portal",
	}
	for i, name := range wantNames {
		if got[i].Name != name {
			t.Fatalf("ProbeRuntime[%d].Name = %q, want %q", i, got[i].Name, name)
		}
		if got[i].Available || got[i].Selected {
			t.Fatalf("ProbeRuntime[%d] available=%v selected=%v, want false/false", i, got[i].Available, got[i].Selected)
		}
		if got[i].Reason == "" {
			t.Fatalf("ProbeRuntime[%d] missing reason", i)
		}
	}
}

func TestOpenRuntimeContextX11ConnectionFailureReturnsNilScreenshotter(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=invalid-display-name"})
	capture, err := OpenRuntimeContext(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "screen/x11: connect") {
		t.Fatalf("missing X11 connection failure: %v", err)
	}
	if capture != nil {
		t.Fatalf("failed X11 constructor leaked typed-nil screenshotter: %T", capture)
	}
}

func TestPublicContextlessConstructorsValidateMissingEndpoints(t *testing.T) {
	constructors := []struct {
		name string
		open func() error
	}{
		{
			name: "GNOME native",
			open: func() error {
				_, err := NewGnomeNativeScreenBackendForRuntime(env.FromEnviron(nil))
				return err
			},
		},
		{
			name: "extension capture",
			open: func() error {
				_, err := NewExtCaptureBackendForSocket("")
				return err
			},
		},
		{
			name: "portal D-Bus",
			open: func() error {
				_, err := NewPortalDBusBackendForBus("")
				return err
			},
		},
		{
			name: "wlroots screencopy",
			open: func() error {
				_, err := NewWlrScreencopyBackendForSocket("")
				return err
			},
		},
	}
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			if err := constructor.open(); err == nil {
				t.Fatal("constructor succeeded without its required endpoint")
			}
		})
	}
}

type rejectingResolutionGrab struct {
	Screenshotter
	cancel  context.CancelFunc
	failure error
}

func (s rejectingResolutionGrab) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.cancel()
	return nil, s.failure
}

// Both the caller's cancellation and a simultaneous capture failure are
// authoritative; one must not erase the other.
func TestResolutionFromGrabRetainsConcurrentCancellationAndCaptureError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cause := errors.New("capture backend lost its output")
	_, _, err := resolutionFromGrab(ctx, rejectingResolutionGrab{cancel: cancel, failure: cause})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("lost one failure: %v", err)
	}
}

func TestValidatedResolutionRetainsConcurrentCancellationAndResolverError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cause := errors.New("output resolution changed mid-probe")
	_, _, err := validatedResolution(ctx, 1920, 1080, cause)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("lost one failure: %v", err)
	}
}

// The fallback may be invoked internally after an earlier availability
// decision. Cancellation must still take precedence at the last boundary
// before capturing the entire desktop, including a backend that ignores ctx.
func TestResolutionFallbackDoesNotCaptureAfterCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &syntheticResolutionCapture{grab: func(context.Context, image.Rectangle) (image.Image, error) {
		t.Fatal("canceled fallback performed full-desktop capture")
		return nil, nil
	}}
	width, height, err := resolutionFromGrab(ctx, backend)
	if width != 0 || height != 0 || !errors.Is(err, context.Canceled) || backend.calls != 0 {
		t.Fatalf("canceled resolution fallback returned %dx%d, error=%v, captures=%d", width, height, err, backend.calls)
	}
}

// Resolution is relied on for capture bounds, cursor positioning, and ROI
// validation. A broken/disconnected resolver must not return impossible output
// sizes as an authoritative desktop dimension.
func TestResolutionWithContextRejectsNonPositiveResolverDimensions(t *testing.T) {
	for _, tc := range []struct {
		name         string
		contextAware bool
		w, h         int
	}{
		{"context zero width", true, 0, 1080},
		{"context zero height", true, 1920, 0},
		{"context negative width", true, -1, 1080},
		{"context negative height", true, 1920, -1},
		{"classic zero width", false, 0, 1080},
		{"classic zero height", false, 1920, 0},
		{"classic negative width", false, -1, 1080},
		{"classic negative height", false, 1920, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var backend Screenshotter
			if tc.contextAware {
				backend = &syntheticContextResolver{width: tc.w, height: tc.h}
			} else {
				backend = &syntheticResolver{width: tc.w, height: tc.h}
			}
			w, h, err := ResolutionWithContext(context.Background(), backend)
			if err == nil || !strings.Contains(err.Error(), "invalid") || w != 0 || h != 0 {
				t.Fatalf("unusable screen %dx%d treated as valid dimensions %dx%d error=%v", tc.w, tc.h, w, h, err)
			}
		})
	}
}

type syntheticResolutionCapture struct {
	grab  func(context.Context, image.Rectangle) (image.Image, error)
	calls int
}

func (b *syntheticResolutionCapture) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	b.calls++
	return b.grab(ctx, rect)
}
func (*syntheticResolutionCapture) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*syntheticResolutionCapture) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*syntheticResolutionCapture) Close() error { return nil }

type syntheticResolver struct {
	syntheticResolutionCapture
	width, height int
	err           error
	after         func()
}

func (b *syntheticResolver) Resolution() (int, int, error) {
	if b.after != nil {
		b.after()
	}
	return b.width, b.height, b.err
}

type syntheticContextResolver struct {
	syntheticResolutionCapture
	width, height int
	err           error
	cancel        func()
}

func (b *syntheticContextResolver) ResolutionWithContext(context.Context) (int, int, error) {
	if b.cancel != nil {
		b.cancel()
	}
	return b.width, b.height, b.err
}
func TestScreenResolutionWithContextHonorsFastPathsAndCancellation(t *testing.T) {
	wantErr := errors.New("synthetic display geometry inaccessible")
	errInvalidResolution := errors.New("expected invalid probe")
	for _, tc := range []struct {
		name         string
		backend      func(context.CancelFunc) Screenshotter
		wantW, wantH int
		wantErr      error
		wantCalls    int
	}{
		{"context resolver", func(context.CancelFunc) Screenshotter { return &syntheticContextResolver{width: 3840, height: 2160} }, 3840, 2160, nil, 0},
		{"context resolver error", func(context.CancelFunc) Screenshotter { return &syntheticContextResolver{err: wantErr} }, 0, 0, wantErr, 0},
		{"context resolver cancels after success", func(cancel context.CancelFunc) Screenshotter {
			return &syntheticContextResolver{width: 1, height: 1, cancel: cancel}
		}, 0, 0, context.Canceled, 0},
		{"classic resolver", func(context.CancelFunc) Screenshotter { return &syntheticResolver{width: 1920, height: 1080} }, 1920, 1080, nil, 0},
		{"classic resolver error", func(context.CancelFunc) Screenshotter { return &syntheticResolver{err: wantErr} }, 0, 0, wantErr, 0},
		{"classic resolver cancels after success", func(cancel context.CancelFunc) Screenshotter {
			return &syntheticResolver{width: 1920, height: 1080, after: cancel}
		}, 0, 0, context.Canceled, 0},
		{"grab fallback", func(context.CancelFunc) Screenshotter {
			return &syntheticResolutionCapture{grab: func(_ context.Context, rect image.Rectangle) (image.Image, error) {
				if !rect.Empty() {
					t.Fatalf("fallback used nonempty region: %v", rect)
				}
				return image.NewRGBA(image.Rect(5, 7, 25, 19)), nil
			}}
		}, 20, 12, nil, 1},
		{"grab failure", func(context.CancelFunc) Screenshotter {
			return &syntheticResolutionCapture{grab: func(context.Context, image.Rectangle) (image.Image, error) { return nil, wantErr }}
		}, 0, 0, wantErr, 1},
		{"nil image", func(context.CancelFunc) Screenshotter {
			return &syntheticResolutionCapture{grab: func(context.Context, image.Rectangle) (image.Image, error) { return nil, nil }}
		}, 0, 0, errInvalidResolution, 1},
		{"zero width image", func(context.CancelFunc) Screenshotter {
			return &syntheticResolutionCapture{grab: func(context.Context, image.Rectangle) (image.Image, error) {
				return image.NewRGBA(image.Rect(0, 0, 0, 5)), nil
			}}
		}, 0, 0, errInvalidResolution, 1},
		{"grab cancels after success", func(cancel context.CancelFunc) Screenshotter {
			return &syntheticResolutionCapture{grab: func(context.Context, image.Rectangle) (image.Image, error) {
				cancel()
				return image.NewRGBA(image.Rect(0, 0, 20, 30)), nil
			}}
		}, 0, 0, context.Canceled, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := tc.backend(cancel)
			w, h, err := ResolutionWithContext(ctx, backend)
			if (tc.wantErr == nil && err != nil) || (tc.wantErr != nil && err == nil) {
				t.Fatalf("resolution err=%v want %v", err, tc.wantErr)
			}
			if errors.Is(tc.wantErr, context.Canceled) && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if errors.Is(tc.wantErr, wantErr) && !errors.Is(err, wantErr) {
				t.Fatalf("lost backend error: %v", err)
			}
			if errors.Is(tc.wantErr, errInvalidResolution) && !strings.Contains(err.Error(), "resolution probe returned") {
				t.Fatalf("missing probe error: %v", err)
			}
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("resolution=%dx%d want %dx%d", w, h, tc.wantW, tc.wantH)
			}
			var calls int
			switch b := backend.(type) {
			case *syntheticContextResolver:
				calls = b.calls
			case *syntheticResolver:
				calls = b.calls
			case *syntheticResolutionCapture:
				calls = b.calls
			}
			if calls != tc.wantCalls {
				t.Fatalf("unexpected framebuffer grab count %d vs %d", calls, tc.wantCalls)
			}
		})
	}
	if _, _, err := ResolutionWithContext(context.Background(), nil); err == nil {
		t.Fatal("nil screenshotter accepted")
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	probe := &syntheticResolutionCapture{grab: func(context.Context, image.Rectangle) (image.Image, error) {
		t.Fatal("canceled request touched framebuffer")
		return nil, nil
	}}
	if _, _, err := ResolutionWithContext(canceled, probe); !errors.Is(err, context.Canceled) || probe.calls != 0 {
		t.Fatalf("canceled resolution probed display: %v", err)
	}
}
func TestScreenOpenRuntimeRealPrivateX11DisplayAndContextBoundaries(t *testing.T) {
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("headless X server not installed: %v", err)
	}
	t.Cleanup(stop)
	auth := filepath.Join(t.TempDir(), "private-auth")
	if lintShadow159 := os.WriteFile(auth, nil, 0600); lintShadow159 != nil {
		t.Fatal(lintShadow159)
	}
	t.Setenv("XAUTHORITY", auth)
	runtime := env.FromEnviron([]string{"DISPLAY=" + display, "XDG_SESSION_TYPE=x11"})
	b, err := OpenRuntimeContext(context.Background(), runtime)
	if err != nil {
		t.Fatalf("private X11 backend failed: %v", err)
	}
	w, h, err := ResolutionWithContext(context.Background(), b)
	if err != nil || w != 1024 || h != 768 {
		t.Fatalf("synthetic Xvfb geometry %dx%d %v", w, h, err)
	}
	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 4, 4)); err != nil {
		t.Fatalf("Xvfb capture failed: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Grab(context.Background(), image.Rect(0, 0, 4, 4)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed X11 capture succeeded: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := OpenRuntimeContext(canceled, runtime); b != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled runtime opened desktop: %v %v", b, err)
	}
}

type cancelingLegacyResolver struct {
	resolutionResolver
	cancel context.CancelFunc
}

func (r cancelingLegacyResolver) Resolution() (int, int, error) { r.cancel(); return 900, 700, nil }

func TestResolutionWithContextRejectsNilImageWithoutPanic(t *testing.T) {
	w, h, err := ResolutionWithContext(context.Background(), &resolutionCancelScreenshotter{})
	if err == nil || w != 0 || h != 0 {
		t.Fatalf("invalid empty capture accepted: %dx%d err=%v", w, h, err)
	}
}
func TestResolutionWithContextRejectsCancellationByLegacyResolver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w, h, err := ResolutionWithContext(ctx, cancelingLegacyResolver{cancel: cancel})
	if !errors.Is(err, context.Canceled) || w != 0 || h != 0 {
		t.Fatalf("canceled resolver accepted: %dx%d err=%v", w, h, err)
	}
}

// An untrusted capture implementation can return an image.Image with a raw
// inverted rectangle or integer-wrapped dimensions. Both must be rejected;
// callers must never observe negative resolution or pass it to allocations.
func TestScreenResolutionFallbackRejectsNegativeOrOverflowedImageBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bounds image.Rectangle
	}{
		{"negative width", image.Rectangle{Min: image.Pt(8, 0), Max: image.Pt(3, 10)}},
		{"negative height", image.Rectangle{Min: image.Pt(0, 8), Max: image.Pt(10, 3)}},
		{"overflowed width", image.Rectangle{Min: image.Pt(math.MinInt, 0), Max: image.Pt(math.MaxInt, 10)}},
		{"overflowed height", image.Rectangle{Min: image.Pt(0, math.MinInt), Max: image.Pt(10, math.MaxInt)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &resolutionCancelScreenshotter{img: &image.RGBA{Rect: tc.bounds}}
			width, height, err := ResolutionWithContext(context.Background(), backend)
			if err == nil || width != 0 || height != 0 || !strings.Contains(err.Error(), "resolution probe") {
				t.Fatalf("malformed geometry %v reported %dx%d, err=%v", tc.bounds, width, height, err)
			}
		})
	}
}

func TestOpenRuntimeContextFailsClosedOnUnavailableSyntheticBackends(t *testing.T) {
	for _, tc := range []struct{ name, desktop, want string }{
		{"kde", "KDE", "KDE requires"},
		{"wlroots", "sway", "wlroots compositor"},
		{"unknown", "unrecognised", "unsupported Wayland compositor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vars := []string{"WAYLAND_DISPLAY=missing-perfuncted-test", "XDG_RUNTIME_DIR=" + t.TempDir(), "XDG_CURRENT_DESKTOP=" + tc.desktop, "DBUS_SESSION_BUS_ADDRESS=unix:path=" + t.TempDir() + "/missing-bus"}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			b, err := OpenRuntimeContext(ctx, env.FromEnviron(vars))
			if b != nil {
				defer b.Close()
				t.Fatalf("unreachable synthetic compositor accepted: %T", b)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %s", err, tc.want)
			}
		})
	}
}
func TestScreenRuntimeConstructorRespectsCanceledAndAbsentDisplays(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b, err := OpenRuntimeContext(ctx, env.FromEnviron([]string{"WAYLAND_DISPLAY=missing"}))
	if b != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open: %T %v", b, err)
	}
	b, err = OpenRuntime(env.FromEnviron(nil))
	if b != nil || err == nil || !strings.Contains(err.Error(), "no display") {
		t.Fatalf("headless open: %T %v", b, err)
	}
}

// These isolated runtimes have no real Wayland socket or session D-Bus. The
// backend selector must preserve compositor-specific failures rather than
// silently falling back to unauthorized desktop capture.
func TestScreenBackendSelectionRequiresAdvertisedCompositorAuthority(t *testing.T) {
	for _, tc := range []struct{ name, desktop, match string }{
		{"wlroots", "sway", "no screencopy protocol"},
		{"KDE", "KDE", "requires KWin"},
		{"unknown", "UnrecognizedSyntheticDesktop", "unsupported Wayland"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := env.FromEnviron([]string{
				"XDG_CURRENT_DESKTOP=" + tc.desktop,
				"WAYLAND_DISPLAY=wayland-no-server",
				"XDG_RUNTIME_DIR=" + filepath.Join(t.TempDir(), "private-runtime"),
				"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/perfuncted-screen-bus",
			})
			backend, err := OpenRuntimeContext(context.Background(), rt)
			if backend != nil {
				_ = backend.Close()
				t.Fatalf("unauthorized backend selected: %T", backend)
			}
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("compositor %q failure was not specific: %v", tc.desktop, err)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			backend, err = OpenRuntimeContext(canceled, rt)
			if backend != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled screen open dispatched: %T, %v", backend, err)
			}
		})
	}
}

func TestScreenResolutionFallbackRejectsCanceledOrMalformedProviderResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := validatedResolution(ctx, 1920, 1080, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled resolver trusted dimensions: %v", err)
	}
	for _, dimensions := range [][2]int{{0, 1080}, {1920, 0}, {-1, 25}} {
		if _, _, err := validatedResolution(context.Background(), dimensions[0], dimensions[1], nil); err == nil {
			t.Fatalf("invalid resolution accepted: %v", dimensions)
		}
	}
	failure := errors.New("synthetic display resolution failure")
	if _, _, err := validatedResolution(context.Background(), 1920, 1080, failure); !errors.Is(err, failure) {
		t.Fatalf("resolver failure lost: %v", err)
	}
}

func TestOpenRuntimePropagatesCancellationDuringCompositorDetection(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var connections atomic.Int32
	probeConnected := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- fmt.Errorf("accept compositor probe: %w", acceptErr)
			return
		}
		connections.Add(1)
		close(probeConnected)
		defer conn.Close()
		if deadlineErr := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
			serverDone <- fmt.Errorf("bound compositor probe observation: %w", deadlineErr)
			return
		}
		if _, readErr := io.Copy(io.Discard, conn); readErr != nil {
			serverDone <- fmt.Errorf("observe canceled compositor probe: %w", readErr)
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=Sway",
	})
	type result struct {
		backend Screenshotter
		err     error
	}
	opened := make(chan result, 1)
	go func() {
		backend, openErr := OpenRuntimeContext(ctx, runtime)
		opened <- result{backend: backend, err: openErr}
	}()

	select {
	case <-probeConnected:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not connect for compositor detection")
	}
	cancel()
	select {
	case got := <-opened:
		if got.backend != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled compositor detection = (%T, %v), want (nil, context.Canceled)", got.backend, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime selection did not return after compositor detection cancellation")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic Wayland peer: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled compositor probe did not close its connection")
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("Wayland connection attempts = %d, want compositor detection only", got)
	}
}

func TestOpenRuntimeSelectsExtCaptureForUnavailablePrimaryBackends(t *testing.T) {
	for _, tc := range []struct {
		name        string
		desktop     string
		additional  []extConstructorAdvertisedGlobal
		connections int
		needsBus    bool
	}{
		{
			name:        "KDE falls back after unavailable KWin",
			desktop:     "KDE",
			additional:  []extConstructorAdvertisedGlobal{{25, "org_kde_plasma_shell", 1}},
			connections: 2,
			needsBus:    true,
		},
		{
			name:        "wlroots falls back after unavailable screencopy",
			desktop:     "other",
			additional:  []extConstructorAdvertisedGlobal{{25, "zwlr_screencopy_manager_v1", 1}},
			connections: 3,
		},
		{
			name:        "unknown Wayland falls back after unavailable screencopy",
			desktop:     "other",
			connections: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverDone := make(chan error, 1)
			go func() {
				for index := 0; index < tc.connections; index++ {
					conn, acceptErr := listener.AcceptUnix()
					if acceptErr != nil {
						serverDone <- fmt.Errorf("accept Wayland connection %d: %w", index+1, acceptErr)
						return
					}
					var additional []extConstructorAdvertisedGlobal
					if index == 0 {
						additional = tc.additional
					}
					if err := serveExtConstructorPeerWithGlobals(conn, "", additional); err != nil {
						serverDone <- fmt.Errorf("Wayland connection %d: %w", index+1, err)
						return
					}
				}
				serverDone <- nil
			}()

			vars := []string{
				"WAYLAND_DISPLAY=" + socket,
				"XDG_CURRENT_DESKTOP=" + tc.desktop,
			}
			if tc.needsBus {
				vars = append(vars, "DBUS_SESSION_BUS_ADDRESS=unix:path="+filepath.Join(t.TempDir(), "absent-bus"))
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			backend, err := OpenRuntimeContext(ctx, env.FromEnviron(vars))
			if err != nil {
				t.Fatalf("runtime did not select available ExtCapture backend: %v", err)
			}
			if _, ok := backend.(*ExtCaptureBackend); !ok {
				t.Fatalf("runtime selected %T, want ExtCaptureBackend", backend)
			}
			if err := backend.Close(); err != nil {
				t.Fatalf("close selected backend: %v", err)
			}
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("synthetic compositor: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("runtime backend did not release its Wayland sessions")
			}
		})
	}
}

func TestScreenRuntimeStopsFallbackWhenExtCaptureSetupIsCanceled(t *testing.T) {
	for _, tc := range []struct {
		name        string
		desktop     string
		globals     []extConstructorAdvertisedGlobal
		connections int
	}{
		{"KDE", "KDE", []extConstructorAdvertisedGlobal{{25, "org_kde_plasma_shell", 1}}, 2},
		{"wlroots", "Sway", []extConstructorAdvertisedGlobal{{25, "zwlr_screencopy_manager_v1", 1}}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := testutil.ShortSocketPath(t, "wayland.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				for i := 0; i < tc.connections; i++ {
					conn, err := listener.AcceptUnix()
					if err != nil {
						done <- err
						return
					}
					if i == tc.connections-1 {
						done <- cancelExtCaptureRegistryRoundTrip(conn, cancel)
						return
					}
					var globals []extConstructorAdvertisedGlobal
					if i == 0 {
						globals = tc.globals
					}
					if err := serveExtConstructorPeerWithGlobals(conn, "", globals); err != nil {
						done <- fmt.Errorf("serve Wayland connection %d: %w", i+1, err)
						return
					}
				}
			}()
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=" + socket,
				"XDG_CURRENT_DESKTOP=" + tc.desktop,
				"DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(t.TempDir(), "missing"),
			})
			backend, err := OpenRuntimeContext(ctx, rt)
			if backend != nil || !errors.Is(err, context.Canceled) {
				if backend != nil {
					_ = backend.Close()
				}
				t.Fatalf("canceled ext-capture setup returned %T, %v", backend, err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("canceled Wayland setup did not close")
			}
		})
	}
}

type cancelKWinScreenProbe struct {
	cancel  context.CancelFunc
	started chan struct{}
	release chan struct{}
}

func (s *cancelKWinScreenProbe) CaptureWorkspace(_ map[string]dbus.Variant, fd dbus.UnixFD) (map[string]dbus.Variant, *dbus.Error) {
	file := os.NewFile(uintptr(fd), "kwin-probe")
	defer file.Close()
	close(s.started)
	s.cancel()
	<-s.release
	return nil, dbus.MakeFailedError(errors.New("probe canceled"))
}

func TestScreenRuntimeStopsFallbackWhenKWinProbeIsCanceled(t *testing.T) {
	_, bus := newSyntheticScreenshotPortal(t)
	reply, err := bus.conn.RequestName(kwinShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim KWin service: reply=%v err=%v", reply, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	probe := &cancelKWinScreenProbe{cancel: cancel, started: make(chan struct{}), release: make(chan struct{})}
	defer close(probe.release)
	if err := bus.conn.Export(probe, dbus.ObjectPath(kwinShotPath), kwinShotIface); err != nil {
		t.Fatal(err)
	}
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=KDE",
		"DBUS_SESSION_BUS_ADDRESS=" + bus.busAddress,
	})
	backend, err := OpenRuntimeContext(ctx, rt)
	if backend != nil || !errors.Is(err, context.Canceled) {
		if backend != nil {
			_ = backend.Close()
		}
		t.Fatalf("canceled KWin probe returned %T, %v", backend, err)
	}
	select {
	case <-probe.started:
	default:
		t.Fatal("KWin authorization probe was not reached")
	}
}

type currentGnomeScreenBridge struct{}

func (currentGnomeScreenBridge) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}

func (currentGnomeScreenBridge) GetExtensionVersion() (string, *dbus.Error) {
	return gnomebridge.ExtensionVersion, nil
}

func (currentGnomeScreenBridge) GetShellVersion() (string, *dbus.Error) {
	return "51.0", nil
}

func (currentGnomeScreenBridge) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityScreen}, nil
}

func startCurrentGnomeScreenBridge(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skipf("private D-Bus server unavailable: %v", err)
	}
	dir, err := os.MkdirTemp("", "pfgn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	address := "unix:path=" + filepath.Join(dir, "bus")
	command := exec.Command("dbus-daemon", "--session", "--nofork", "--nopidfile", "--address="+address, "--print-address=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read private D-Bus address: %v", err)
	}
	address = strings.TrimSpace(line)
	service, err := dbusutil.SessionBusAddressContext(context.Background(), address)
	if err != nil {
		t.Fatalf("connect private bridge service: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME bridge name: reply=%v err=%v", reply, err)
	}
	if err := service.Export(currentGnomeScreenBridge{}, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatalf("export current GNOME screen bridge: %v", err)
	}
	return address, dir
}

func TestScreenRuntimeSelectsCurrentGnomeNativeBridge(t *testing.T) {
	address, runtimeDir := startCurrentGnomeScreenBridge(t)
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	backend, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("open GNOME screen runtime with current bridge: %v", err)
	}
	native, ok := backend.(*GnomeNativeScreenBackend)
	if !ok {
		_ = backend.Close()
		t.Fatalf("GNOME runtime selected %T, want native bridge", backend)
	}
	if !native.CanonicalHashing() {
		t.Fatal("GNOME native backend did not advertise canonical hashes")
	}
	if err := native.Close(); err != nil {
		t.Fatalf("close GNOME native backend: %v", err)
	}
}

type cancelGnomeScreenNegotiation struct {
	cancel  context.CancelFunc
	started chan struct{}
	release chan struct{}
}

func (s *cancelGnomeScreenNegotiation) GetProtocolVersion() (uint32, *dbus.Error) {
	close(s.started)
	s.cancel()
	<-s.release
	return gnomebridge.ProtocolVersion, nil
}

func TestScreenRuntimeCancellationStopsGNOMEFallbackAfterBridgeNegotiation(t *testing.T) {
	_, bus := newSyntheticScreenshotPortal(t)
	reply, err := bus.conn.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME bridge name: reply=%v err=%v", reply, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	service := &cancelGnomeScreenNegotiation{
		cancel:  cancel,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(service.release)
	if err := bus.conn.Export(service, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatalf("export canceling GNOME bridge core: %v", err)
	}
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-missing",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + bus.busAddress,
	})

	backend, err := OpenRuntimeContext(ctx, runtime)
	if backend != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GNOME runtime selected %T with error %v", backend, err)
	}
	select {
	case <-service.started:
	default:
		t.Fatal("GNOME bridge negotiation did not trigger cancellation")
	}
}

type outdatedGnomeScreenBridge struct{}

func (outdatedGnomeScreenBridge) GetProtocolVersion() (uint32, *dbus.Error) {
	return gnomebridge.ProtocolVersion, nil
}

func (outdatedGnomeScreenBridge) GetExtensionVersion() (string, *dbus.Error) {
	return "0", nil
}

func (outdatedGnomeScreenBridge) GetShellVersion() (string, *dbus.Error) {
	return "51.0", nil
}

func (outdatedGnomeScreenBridge) GetCapabilities() ([]string, *dbus.Error) {
	return []string{gnomebridge.CapabilityScreen}, nil
}

func startPrivateOutdatedGnomeBridge(t *testing.T) string {
	t.Helper()
	dbusDaemon, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skipf("private D-Bus server unavailable: %v", err)
	}
	cmd := exec.Command(dbusDaemon, "--session", "--nofork", "--nopidfile", "--print-address=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("open private bus address pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start private D-Bus server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	addressLine, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read private D-Bus address: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}
	address := strings.TrimSpace(addressLine)
	service, err := dbusutil.SessionBusAddressContext(context.Background(), address)
	if err != nil {
		t.Fatalf("connect private D-Bus service: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(gnomebridge.BusName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim GNOME bridge name: reply=%v err=%v", reply, err)
	}
	if err := service.Export(outdatedGnomeScreenBridge{}, dbus.ObjectPath(gnomebridge.ObjectPath), gnomebridge.CoreInterface); err != nil {
		t.Fatalf("export outdated GNOME bridge methods: %v", err)
	}
	return address
}

func TestGNOMEScreenRuntimeReturnsRestartRequiredInsteadOfFallingBack(t *testing.T) {
	address := startPrivateOutdatedGnomeBridge(t)
	bin := t.TempDir()
	oldPath := os.Getenv("PATH")
	path := bin + string(os.PathListSeparator) + oldPath
	t.Setenv("PATH", path)
	settingsLog := filepath.Join(t.TempDir(), "gsettings.log")
	settings := "#!/bin/sh\nset -eu\nprintf '%s\\n' \"$*\" >> \"$PF_TEST_GSETTINGS_LOG\"\n" +
		"case \"$*\" in\n" +
		"  'get org.gnome.shell allow-extension-installation') printf 'true\\n' ;;\n" +
		"  'get org.gnome.shell disable-user-extensions') printf 'false\\n' ;;\n" +
		"  'get org.gnome.shell disabled-extensions') printf '[]\\n' ;;\n" +
		"  'get org.gnome.shell enabled-extensions') printf '[]\\n' ;;\n" +
		"  'set org.gnome.shell enabled-extensions '*) exit 0 ;;\n" +
		"  *) exit 91 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "gsettings"), []byte(settings), 0o700); err != nil {
		t.Fatalf("write isolated gsettings helper: %v", err)
	}
	t.Setenv("PF_TEST_GSETTINGS_LOG", settingsLog)

	dataHome := t.TempDir()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-not-present",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
		"XDG_DATA_HOME=" + dataHome,
		"PATH=" + path,
		"PF_TEST_GSETTINGS_LOG=" + settingsLog,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	backend, err := OpenRuntimeContext(ctx, runtime)
	if backend != nil {
		_ = backend.Close()
	}
	log, logErr := os.ReadFile(settingsLog)
	if backend != nil || !errors.Is(err, gnomebridge.ErrSessionRestartRequired) {
		t.Fatalf("GNOME restart requirement was replaced by fallback: backend=%T err=%v gsettings=%q logErr=%v", backend, err, log, logErr)
	}
	if !strings.Contains(err.Error(), "log out and back in once") {
		t.Fatalf("restart error omitted its actionable instruction: %v", err)
	}
	if logErr != nil {
		t.Fatalf("read isolated settings calls: %v", logErr)
	}
	wantLog := "get org.gnome.shell allow-extension-installation\n" +
		"get org.gnome.shell disable-user-extensions\n" +
		"get org.gnome.shell disabled-extensions\n" +
		"get org.gnome.shell enabled-extensions\n" +
		"set org.gnome.shell enabled-extensions ['perfuncted@nskaggs.github.io']\n"
	if string(log) != wantLog {
		t.Fatalf("extension setup used unexpected settings operations: %q", log)
	}
	extensionPath := filepath.Join(dataHome, "gnome-shell", "extensions", "perfuncted@nskaggs.github.io")
	if info, err := os.Stat(extensionPath); err != nil || !info.IsDir() {
		t.Fatalf("private test extension was not installed before restart was requested: stat=%v err=%v", info, err)
	}
}

func TestScreenRuntimeSelectsAuthorizedGnomeShellCaptureBeforePortal(t *testing.T) {
	_, portal := newSyntheticScreenshotPortal(t)
	shell := &syntheticGnomeScreenshot{success: true}
	reply, err := portal.conn.RequestName(gnomeShellShotDest, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim synthetic GNOME Shell screenshot service: reply=%v err=%v", reply, err)
	}
	if err := portal.conn.Export(shell, dbus.ObjectPath(gnomeShellShotPath), gnomeShellShotIface); err != nil {
		t.Fatalf("export synthetic GNOME Shell screenshot service: %v", err)
	}

	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=wayland-without-synthetic-bridge",
		"XDG_RUNTIME_DIR=" + t.TempDir(),
		"XDG_CURRENT_DESKTOP=GNOME",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	backend, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("runtime failed to select the available GNOME Shell backend: %v", err)
	}
	defer backend.Close()
	if _, ok := backend.(*GnomeShellScreenshotBackend); !ok {
		t.Fatalf("runtime selected %T, want the authorized GNOME Shell backend", backend)
	}
	img, err := backend.Grab(ctx, image.Rectangle{})
	if err != nil {
		t.Fatalf("synthetic GNOME Shell capture: %v", err)
	}
	if img == nil {
		t.Fatal("synthetic GNOME Shell capture returned nil image")
	}
	if img.Bounds().Size() != image.Pt(4, 4) {
		t.Fatalf("synthetic GNOME Shell capture bounds=%v", img.Bounds())
	}
	shell.mu.Lock()
	captures := append([]string(nil), shell.observed...)
	shell.mu.Unlock()
	if len(captures) < 2 || captures[0] != "area" || captures[len(captures)-1] != "full" {
		t.Fatalf("runtime authorization and capture sequence = %v, want area probe then full capture", captures)
	}
}

// Real private D-Bus providers exercise the runtime fallback ordering without
// browsing, authorizing or capturing the actual desktop.
func TestScreenRuntimeSelectsKWinScreenshotOverFallback(t *testing.T) {
	address, provider := startSyntheticKWinBus(t, true)
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/nonexistent/perfuncted-screen-wire",
		"XDG_CURRENT_DESKTOP=KDE",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	backend, err := OpenRuntimeContext(ctx, rt)
	if err != nil {
		t.Fatalf("authoritative KDE screenshot service not selected: %v", err)
	}
	defer backend.Close()
	if _, ok := backend.(*KWinShotBackend); !ok {
		t.Fatalf("KDE selected %T rather than KWin FD screenshot protocol", backend)
	}
	img, err := backend.Grab(ctx, image.Rect(0, 0, 2, 2))
	if err != nil || img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 {
		t.Fatalf("runtime KWin screenshot failed: %v image=%v", err, img)
	}
	provider.mu.Lock()
	count := len(provider.captures)
	provider.mu.Unlock()
	if count != 2 {
		t.Fatalf("KWin initialization/capture count=%d, want two", count)
	}
}
func TestScreenRuntimeFallbackToAuthorizedSyntheticPortalAcrossCompositors(t *testing.T) {
	for _, desktop := range []string{"KDE", "GNOME", "other"} {
		t.Run(desktop, func(t *testing.T) {
			_, portal := newSyntheticScreenshotPortal(t)
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/nonexistent/perfuncted-screen-wire",
				"XDG_CURRENT_DESKTOP=" + desktop,
				"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			backend, err := OpenRuntimeContext(ctx, rt)
			if err != nil {
				t.Fatalf("%s runtime did not reach available portal: %v", desktop, err)
			}
			defer backend.Close()
			if _, ok := backend.(*PortalDBusBackend); !ok {
				t.Fatalf("%s runtime selected %T instead of private portal provider", desktop, backend)
			}
			// The portal exists, but no screenshot has been authorized or supplied.
			// Successful backend selection must not manufacture captured pixels.
			image, err := backend.Grab(ctx, image.Rect(0, 0, 1, 1))
			if err == nil || image != nil {
				t.Fatalf("%s portal fabricated screenshot from empty URI: %v %v", desktop, image, err)
			}
		})
	}
}
func TestScreenRuntimeAbsentPrivateBusFailsClosedAcrossGNOMEAndKDE(t *testing.T) {
	for _, desktop := range []string{"GNOME", "KDE"} {
		t.Run(desktop, func(t *testing.T) {
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=missing-perfuncted-wayland",
				"XDG_RUNTIME_DIR=" + t.TempDir(),
				"XDG_CURRENT_DESKTOP=" + desktop,
				"DBUS_SESSION_BUS_ADDRESS=unix:path=" + t.TempDir() + "/absent-bus",
			})
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			backend, err := OpenRuntimeContext(ctx, rt)
			if backend != nil {
				defer backend.Close()
				t.Fatalf("%s opened unsupported desktop without an authenticated provider", desktop)
			}
			if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), desktop) {
				t.Fatalf("%s failed to report unavailable backend deterministically: %v", desktop, err)
			}
		})
	}
}

func TestOpenRuntimeUnknownCompositorReturnsCancellationDuringExtCaptureSetup(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		for index := 0; index < 3; index++ {
			conn, acceptErr := listener.AcceptUnix()
			if acceptErr != nil {
				serverDone <- acceptErr
				return
			}
			if index < 2 {
				if serveErr := serveExtConstructorPeerWithGlobals(conn, "", nil); serveErr != nil {
					serverDone <- serveErr
					return
				}
				continue
			}
			serverDone <- cancelExtCaptureRegistryRoundTrip(conn, cancel)
			return
		}
	}()

	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=UnrecognizedSyntheticDesktop",
	})
	backend, err := OpenRuntimeContext(ctx, runtime)
	if backend != nil {
		_ = backend.Close()
		t.Fatal("canceled ext-capture setup returned a screenshot backend")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime error = %v, want context.Canceled", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ext-capture setup did not close its canceled registry connection")
	}
}

func cancelExtCaptureRegistryRoundTrip(conn *net.UnixConn, cancel context.CancelFunc) error {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	for {
		var header [8]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return fmt.Errorf("read ext-capture setup request: %w", err)
		}
		size := int(wl.Uint32(header[4:]) >> 16)
		if size < 8 || size > 16384 {
			return fmt.Errorf("invalid ext-capture setup request size %d", size)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			return fmt.Errorf("read ext-capture setup request body: %w", err)
		}
		if wl.Uint32(header[:4]) == 1 && wl.Uint32(header[4:])&0xffff == 0 {
			cancel()
			var unexpected [1]byte
			if _, err := conn.Read(unexpected[:]); !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("canceled client did not close its registry connection: %w", err)
			}
			return nil
		}
	}
}

func TestOpenRuntimeSelectsWlrScreencopyFromAdvertisedManager(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			conn, acceptErr := listener.AcceptUnix()
			if acceptErr != nil {
				serverDone <- fmt.Errorf("accept Wayland registry connection %d: %w", i+1, acceptErr)
				return
			}
			globals := []extConstructorAdvertisedGlobal{{25, "zwlr_screencopy_manager_v1", 3}}
			if serveErr := serveExtConstructorPeerWithGlobals(conn, "", globals); serveErr != nil {
				serverDone <- fmt.Errorf("serve Wayland registry connection %d: %w", i+1, serveErr)
				return
			}
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=Other",
	})
	backend, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("open WLR runtime with advertised screencopy manager: %v", err)
	}
	wlr, ok := backend.(*WlrScreencopyBackend)
	if !ok {
		_ = backend.Close()
		t.Fatalf("runtime selected %T, want WlrScreencopyBackend", backend)
	}
	if !wlr.CanonicalHashing() {
		t.Fatal("selected WLR backend did not advertise canonical hashes")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close selected WLR backend: %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("runtime did not close both registry connections")
	}
}

func TestOpenRuntimeUsesWlrProtocolForUnknownCompositor(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		for index := 0; index < 2; index++ {
			conn, acceptErr := listener.AcceptUnix()
			if acceptErr != nil {
				serverDone <- fmt.Errorf("accept Wayland connection %d: %w", index+1, acceptErr)
				return
			}
			var globals []extConstructorAdvertisedGlobal
			if index == 1 {
				globals = []extConstructorAdvertisedGlobal{{25, "zwlr_screencopy_manager_v1", 3}}
			}
			if serveErr := serveExtConstructorPeerWithGlobals(conn, "", globals); serveErr != nil {
				serverDone <- fmt.Errorf("serve Wayland connection %d: %w", index+1, serveErr)
				return
			}
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=UnrecognizedSyntheticDesktop",
	})
	backend, err := OpenRuntimeContext(ctx, runtime)
	if err != nil {
		t.Fatalf("open WLR runtime after unknown compositor probe: %v", err)
	}
	wlr, ok := backend.(*WlrScreencopyBackend)
	if !ok {
		_ = backend.Close()
		t.Fatalf("runtime selected %T, want WlrScreencopyBackend", backend)
	}
	if !wlr.CanonicalHashing() {
		t.Fatal("selected WLR backend did not advertise canonical hashes")
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close selected WLR backend: %v", err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("runtime did not complete protocol discovery and setup")
	}
}

func TestOpenRuntimeStopsFallbackWhenWlrootsSetupIsCanceled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		desktop string
	}{
		{name: "explicit wlroots compositor", desktop: "Sway"},
		{name: "unknown Wayland compositor", desktop: "Other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertRuntimeStopsFallbackWhenPrimaryWaylandSetupIsCanceled(t, tc.desktop)
		})
	}
}

func assertRuntimeStopsFallbackWhenPrimaryWaylandSetupIsCanceled(t *testing.T, desktop string) {
	t.Helper()
	socket := testutil.ShortSocketPath(t, "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var connections atomic.Int32
	secondConnected := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		probe, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- fmt.Errorf("accept compositor probe: %w", acceptErr)
			return
		}
		connections.Add(1)
		if serveErr := serveExtConstructorPeer(probe, ""); serveErr != nil {
			serverDone <- fmt.Errorf("serve compositor probe: %w", serveErr)
			return
		}

		setup, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- fmt.Errorf("accept wlroots backend setup: %w", acceptErr)
			return
		}
		connections.Add(1)
		close(secondConnected)
		defer setup.Close()
		if deadlineErr := setup.SetReadDeadline(time.Now().Add(3 * time.Second)); deadlineErr != nil {
			serverDone <- fmt.Errorf("bound backend setup observation: %w", deadlineErr)
			return
		}
		if _, readErr := io.Copy(io.Discard, setup); readErr != nil {
			serverDone <- fmt.Errorf("observe canceled wlroots setup: %w", readErr)
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + socket,
		"XDG_CURRENT_DESKTOP=" + desktop,
	})
	type result struct {
		backend Screenshotter
		err     error
	}
	opened := make(chan result, 1)
	go func() {
		backend, openErr := OpenRuntimeContext(ctx, runtime)
		opened <- result{backend: backend, err: openErr}
	}()

	select {
	case <-secondConnected:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not start wlroots backend setup after compositor detection")
	}
	cancel()
	select {
	case got := <-opened:
		if got.backend != nil || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("canceled backend selection = (%T, %v), want (nil, context.Canceled)", got.backend, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runtime selection did not return after backend setup cancellation")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled Wayland setup did not close its connection")
	}
	if got := connections.Load(); got != 2 {
		t.Fatalf("Wayland connection attempts = %d, want detection plus canceled primary backend only", got)
	}
}

func TestScreenPortalProbeDistinguishesConnectedBusWithoutProviderFromAuthorizedProvider(t *testing.T) {
	address, _ := startSyntheticKWinBus(t, false)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	absent := checkPortalDbus(rt)
	if absent.Available || absent.Reason != "org.freedesktop.portal.Desktop not on session bus" {
		t.Fatalf("unregistered portal advertised: %+v", absent)
	}
	_, provider := newSyntheticScreenshotPortal(t)
	available := checkPortalDbus(env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + provider.busAddress}))
	if !available.Available || !strings.Contains(available.Reason, "on session bus") {
		t.Fatalf("healthy private portal not discovered: %+v", available)
	}
}

func TestScreenKWinProbeVerifiesRealPrivateServiceAuthorization(t *testing.T) {
	address, provider := startSyntheticKWinBus(t, true)
	rt := env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=" + address})
	available := checkKWinShot(rt, compositor.KDE)
	if !available.Available || available.Name != "kwin-shot2" {
		t.Fatalf("authorized private KWin was denied: %+v", available)
	}
	provider.mu.Lock()
	received := len(provider.captures)
	provider.mu.Unlock()
	if received == 0 {
		t.Fatalf("probe claimed screenshot without verifying capture")
	}
}

type resolutionCancelScreenshotter struct {
	img    image.Image
	cancel context.CancelFunc
}

type resolutionResolver struct{}

type contextResolutionResolver struct{}

type cancelingContextResolutionResolver struct {
	cancel context.CancelFunc
}

func (resolutionResolver) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 11, 7)), nil
}

func (resolutionResolver) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (resolutionResolver) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (resolutionResolver) Resolution() (int, int, error) { return 11, 7, nil }

func (resolutionResolver) Close() error { return nil }

func (contextResolutionResolver) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 17, 13)), nil
}

func (contextResolutionResolver) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (contextResolutionResolver) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (contextResolutionResolver) Resolution() (int, int, error) { return 1, 1, nil }

func (contextResolutionResolver) ResolutionWithContext(ctx context.Context) (int, int, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	return 17, 13, nil
}

func (contextResolutionResolver) Close() error { return nil }

func (r cancelingContextResolutionResolver) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
}

func (r cancelingContextResolutionResolver) GrabFullHash(context.Context) (uint32, error) {
	return 0, nil
}

func (r cancelingContextResolutionResolver) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (r cancelingContextResolutionResolver) Resolution() (int, int, error) { return 1, 1, nil }

func (r cancelingContextResolutionResolver) ResolutionWithContext(context.Context) (int, int, error) {
	r.cancel()
	return 17, 13, nil
}

func (r cancelingContextResolutionResolver) Close() error { return nil }

func (s *resolutionCancelScreenshotter) cancelOnce() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *resolutionCancelScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.cancelOnce()
	return s.img, nil
}

func (s *resolutionCancelScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	return 0, nil
}

func (s *resolutionCancelScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	return 0, nil
}

func (s *resolutionCancelScreenshotter) Close() error { return nil }

func TestResolutionWithContext_CanceledAfterGrab(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	img := image.NewRGBA(image.Rect(0, 0, 11, 7))
	sc := &resolutionCancelScreenshotter{img: img, cancel: cancel}

	w, h, err := ResolutionWithContext(ctx, sc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolutionWithContext error = %v, want context.Canceled", err)
	}
	if w != 0 || h != 0 {
		t.Fatalf("ResolutionWithContext returned %dx%d on cancellation, want 0x0", w, h)
	}
}

func TestResolutionWithContext_NilScreenshotter(t *testing.T) {
	var sc *resolutionCancelScreenshotter

	w, h, err := ResolutionWithContext(context.Background(), Screenshotter(sc))
	if err == nil {
		t.Fatal("expected error for nil screenshotter")
	}
	if w != 0 || h != 0 {
		t.Fatalf("ResolutionWithContext returned %dx%d for nil screenshotter, want 0x0", w, h)
	}
}

func TestResolutionWithContext_CanceledResolver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := ResolutionWithContext(ctx, resolutionResolver{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolutionWithContext error = %v, want context.Canceled", err)
	}
}

func TestResolutionWithContext_PrefersContextAwareResolver(t *testing.T) {
	w, h, err := ResolutionWithContext(context.Background(), contextResolutionResolver{})
	if err != nil {
		t.Fatalf("ResolutionWithContext error = %v", err)
	}
	if w != 17 || h != 13 {
		t.Fatalf("ResolutionWithContext returned %dx%d, want 17x13", w, h)
	}
}

func TestResolutionWithContext_RejectsCancellationAfterResolver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sc := cancelingContextResolutionResolver{cancel: cancel}

	w, h, err := ResolutionWithContext(ctx, sc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ResolutionWithContext error = %v, want context.Canceled", err)
	}
	if w != 0 || h != 0 {
		t.Fatalf("ResolutionWithContext returned %dx%d on cancellation, want 0x0", w, h)
	}
}

// Selecting X11 must prove that the supplied display endpoint is usable.
// An explicit but nonexistent display must not publish a screenshot backend.
func TestScreenOpenRuntimeRejectsUnreachableExplicitX11Display(t *testing.T) {
	rt := env.FromEnviron([]string{"DISPLAY=:999999", "XDG_SESSION_TYPE=x11"})
	b, err := OpenRuntimeContext(context.Background(), rt)
	if b != nil || err == nil {
		t.Fatalf("unreachable X11 display published screenshot backend=%T err=%v", b, err)
	}
}

// Cancellation takes precedence over backend selection even with no usable
// compositor endpoint; never turn cancellation into an availability fallback.
func TestScreenOpenRuntimeCanceledBeforeCompositorDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt := env.FromEnviron([]string{"DISPLAY=:999999", "XDG_SESSION_TYPE=x11"})
	b, err := OpenRuntimeContext(ctx, rt)
	if b != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled discovery returned backend=%T err=%v", b, err)
	}
	if err != nil && strings.Contains(err.Error(), "dial") {
		t.Fatalf("canceled discovery attempted X11 dial: %v", err)
	}
}
