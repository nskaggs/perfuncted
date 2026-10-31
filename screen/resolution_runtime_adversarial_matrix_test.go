package screen

import (
	"context"
	"errors"
	"image"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

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
