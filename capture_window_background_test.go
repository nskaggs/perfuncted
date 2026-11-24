package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

type windowPixmapScreenshotter struct {
	*windowCaptureScreenshotter
	capture func(context.Context, string) (screen.WindowPixmapCapture, error)
}

func (s *windowPixmapScreenshotter) CaptureWindowPixmap(ctx context.Context, id string) (screen.WindowPixmapCapture, error) {
	return s.capture(ctx, id)
}

func TestCaptureWindowBackgroundReportsTargetOnlyStateWithoutActivation(t *testing.T) {
	info := window.Info{NativeID: "0x2a", Title: "Editor", AppID: "org.example.Editor", PID: 41,
		X: 10, Y: 20, W: 40, H: 30}
	manager := newHandleWindowManager(info)
	grabCalls, pixmapCalls := 0, 0
	base := &windowCaptureScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
		grabCalls++
		return nil, errors.New("visible screen crop must not be used")
	}}
	backend := &windowPixmapScreenshotter{windowCaptureScreenshotter: base, capture: func(_ context.Context, id string) (screen.WindowPixmapCapture, error) {
		pixmapCalls++
		if id != info.NativeID {
			t.Fatalf("backend target ID=%q, want %q", id, info.NativeID)
		}
		return screen.WindowPixmapCapture{
			Image: image.NewRGBA(image.Rect(0, 0, 40, 30)), ScreenRect: image.Rect(10, 20, 50, 50),
			Visibility: screen.WindowPixmapOccluded,
		}, nil
	}}
	session := NewSessionForTesting(backend, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := session.Screen.CaptureWindowBackground(context.Background(), target)
	if err != nil {
		t.Fatalf("CaptureWindowBackground: %v", err)
	}
	if capture.Mode != WindowCaptureBackgroundPixmap || capture.State != WindowCaptureOccluded || capture.WindowID != info.NativeID {
		t.Fatalf("capture metadata=%+v", capture)
	}
	if capture.ScreenRect != image.Rect(10, 20, 50, 50) || capture.Capture.Image.Bounds() != image.Rect(0, 0, 40, 30) {
		t.Fatalf("capture geometry=%v image=%v", capture.ScreenRect, capture.Capture.Image.Bounds())
	}
	if pixmapCalls != 1 || grabCalls != 0 || len(manager.actions) != 0 {
		t.Fatalf("pixmap=%d visible-crop=%d window-actions=%v", pixmapCalls, grabCalls, manager.actions)
	}
}

func TestCaptureWindowBackgroundRequiresKnownFocusForBackgroundVisibleState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		foreground bool
		known      bool
		want       WindowCaptureState
	}{
		{name: "known inactive", known: true, want: WindowCaptureBackgroundVisible},
		{name: "known foreground", foreground: true, known: true, want: WindowCaptureForeground},
		{name: "focus property unavailable", want: WindowCaptureStateUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := window.Info{NativeID: "42", Title: "Editor", X: 10, Y: 20, W: 4, H: 3}
			manager := newHandleWindowManager(info)
			base := &windowCaptureScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
				return nil, errors.New("visible crop must not be used")
			}}
			backend := &windowPixmapScreenshotter{windowCaptureScreenshotter: base, capture: func(context.Context, string) (screen.WindowPixmapCapture, error) {
				return screen.WindowPixmapCapture{Image: image.NewRGBA(image.Rect(0, 0, 4, 3)), ScreenRect: image.Rect(10, 20, 14, 23),
					Visibility: screen.WindowPixmapUnobscured, Foreground: tc.foreground, ForegroundKnown: tc.known}, nil
			}}
			session := NewSessionForTesting(backend, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
			if err != nil {
				t.Fatal(err)
			}
			capture, err := session.Screen.CaptureWindowBackground(context.Background(), target)
			if err != nil {
				t.Fatal(err)
			}
			if capture.State != tc.want {
				t.Fatalf("state=%q, want %q", capture.State, tc.want)
			}
		})
	}
}

func TestCaptureWindowBackgroundRejectsMinimizedAndUnsupportedBackends(t *testing.T) {
	for _, tc := range []struct {
		name    string
		info    window.Info
		backend func() screen.Screenshotter
	}{
		{name: "minimized", info: window.Info{NativeID: "min", Title: "Editor", Minimized: true}},
		{name: "backend lacks target-only capture", info: window.Info{NativeID: "other", Title: "Editor"}, backend: func() screen.Screenshotter {
			return &windowCaptureScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
				return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newHandleWindowManager(tc.info)
			pixmapCalls := 0
			var backend screen.Screenshotter
			if tc.backend != nil {
				backend = tc.backend()
			} else {
				base := &windowCaptureScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
					return nil, errors.New("should not capture")
				}}
				backend = &windowPixmapScreenshotter{windowCaptureScreenshotter: base, capture: func(context.Context, string) (screen.WindowPixmapCapture, error) {
					pixmapCalls++
					return screen.WindowPixmapCapture{}, nil
				}}
			}
			session := NewSessionForTesting(backend, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.Screen.CaptureWindowBackground(context.Background(), target)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("CaptureWindowBackground error=%v, want ErrUnsupported", err)
			}
			if tc.name == "minimized" && !errors.Is(err, ErrWindowCaptureMinimized) {
				t.Fatalf("minimized error=%v, want ErrWindowCaptureMinimized", err)
			}
			if pixmapCalls != 0 || len(manager.actions) != 0 {
				t.Fatalf("pixmap calls=%d actions=%v", pixmapCalls, manager.actions)
			}
		})
	}
}

func TestCaptureWindowBackgroundPreservesUnmappedState(t *testing.T) {
	info := window.Info{NativeID: "unmapped", Title: "Editor", X: 1, Y: 2, W: 3, H: 4}
	manager := newHandleWindowManager(info)
	base := &windowCaptureScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
		return nil, errors.New("visible crop must not be used")
	}}
	backend := &windowPixmapScreenshotter{windowCaptureScreenshotter: base, capture: func(context.Context, string) (screen.WindowPixmapCapture, error) {
		return screen.WindowPixmapCapture{}, errors.Join(screen.ErrBackgroundWindowCaptureUnsupported, screen.ErrBackgroundWindowUnmapped)
	}}
	session := NewSessionForTesting(backend, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Screen.CaptureWindowBackground(context.Background(), target); !errors.Is(err, ErrUnsupported) || !errors.Is(err, ErrWindowCaptureUnmapped) {
		t.Fatalf("unmapped target error=%v", err)
	}
}

func TestCaptureWindowBackgroundToWritesPNGAndReturnsMetadata(t *testing.T) {
	info := window.Info{NativeID: "editor", Title: "Editor", X: 3, Y: 4, W: 5, H: 2}
	manager := newHandleWindowManager(info)
	backend := &windowPixmapScreenshotter{
		windowCaptureScreenshotter: &windowCaptureScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
			return nil, errors.New("visible crop must not be used")
		}},
		capture: func(context.Context, string) (screen.WindowPixmapCapture, error) {
			return screen.WindowPixmapCapture{
				Image: image.NewRGBA(image.Rect(0, 0, info.W, info.H)), ScreenRect: image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H),
				Visibility: screen.WindowPixmapOccluded,
			}, nil
		},
	}
	session := NewSessionForTesting(backend, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "background.png")
	capture, err := session.Screen.CaptureWindowBackgroundTo(context.Background(), target, path)
	if err != nil {
		t.Fatalf("CaptureWindowBackgroundTo: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output PNG: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode output PNG: %v", err)
	}
	if decoded.Bounds().Dx() != info.W || decoded.Bounds().Dy() != info.H || capture.WindowID != "editor" || capture.Mode != WindowCaptureBackgroundPixmap {
		t.Fatalf("PNG bounds=%v capture=%+v", decoded.Bounds(), capture)
	}
}

func TestCaptureWindowBackgroundToRejectsEmptyPathBeforeCaptureAndPropagatesFailures(t *testing.T) {
	captureErr := errors.New("background capture failed")
	for _, tc := range []struct {
		name         string
		path         func(*testing.T) string
		captureErr   error
		wantErr      error
		wantCaptures int
	}{
		{name: "empty path", path: func(*testing.T) string { return "" }, wantErr: ErrInvalidArgument},
		{name: "capture failure", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "capture.png") }, captureErr: captureErr, wantErr: captureErr, wantCaptures: 1},
		{name: "publish failure", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing", "capture.png") }, wantCaptures: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := window.Info{NativeID: "editor", Title: "Editor", X: 1, Y: 2, W: 3, H: 2}
			manager := newHandleWindowManager(info)
			captures := 0
			backend := &windowPixmapScreenshotter{
				windowCaptureScreenshotter: &windowCaptureScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
					return nil, errors.New("visible crop must not be used")
				}},
				capture: func(context.Context, string) (screen.WindowPixmapCapture, error) {
					captures++
					if tc.captureErr != nil {
						return screen.WindowPixmapCapture{}, tc.captureErr
					}
					return screen.WindowPixmapCapture{
						Image: image.NewRGBA(image.Rect(0, 0, info.W, info.H)), ScreenRect: image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H),
						Visibility: screen.WindowPixmapOccluded,
					}, nil
				},
			}
			session := NewSessionForTesting(backend, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.Screen.CaptureWindowBackgroundTo(context.Background(), target, tc.path(t))
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("CaptureWindowBackgroundTo error=%v, want error matching %v", err, tc.wantErr)
			}
			if captures != tc.wantCaptures {
				t.Fatalf("background captures=%d, want %d", captures, tc.wantCaptures)
			}
		})
	}
}
