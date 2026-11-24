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

type windowCaptureScreenshotter struct {
	grab func(context.Context, image.Rectangle) (image.Image, error)
}

func (s *windowCaptureScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return s.grab(ctx, rect)
}

func (*windowCaptureScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (*windowCaptureScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (*windowCaptureScreenshotter) Close() error { return nil }

var _ screen.Screenshotter = (*windowCaptureScreenshotter)(nil)

func windowCaptureFixture(
	t *testing.T,
	info window.Info,
	grab func(context.Context, image.Rectangle) (image.Image, error),
) (*Session, *handleWindowManager, *windowCaptureScreenshotter) {
	t.Helper()
	manager := newHandleWindowManager(info)
	screenshot := &windowCaptureScreenshotter{grab: grab}
	session := NewSessionForTesting(screenshot, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	return session, manager, screenshot
}

func TestCaptureWindowUsesFreshBoundsAndDoesNotActivate(t *testing.T) {
	info := window.Info{
		NativeID: "editor-1", Title: "Editor", AppID: "org.example.Editor", PID: 41,
		X: -20, Y: 30, W: 40, H: 25,
	}
	want := image.Rect(-20, 30, 20, 55)
	var got image.Rectangle
	grabs := 0
	session, manager, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		got = rect
		grabs++
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}

	capture, err := session.Screen.CaptureWindow(context.Background(), target)
	if err != nil {
		t.Fatalf("CaptureWindow: %v", err)
	}
	if got != want || capture.ScreenRect != want || capture.Capture.ScreenRect != want {
		t.Fatalf("requested rect=%v metadata rect=%v capture rect=%v, want %v", got, capture.ScreenRect, capture.Capture.ScreenRect, want)
	}
	if capture.Capture.Image.Bounds().Dx() != want.Dx() || capture.Capture.Image.Bounds().Dy() != want.Dy() {
		t.Fatalf("image bounds=%v, want %dx%d crop", capture.Capture.Image.Bounds(), want.Dx(), want.Dy())
	}
	if capture.PixelBounds != capture.Capture.Image.Bounds() || capture.PixelScaleX != 1 || capture.PixelScaleY != 1 {
		t.Fatalf("pixel provenance bounds=%v scale=(%v,%v)", capture.PixelBounds, capture.PixelScaleX, capture.PixelScaleY)
	}
	if capture.WindowID != "editor-1" || capture.Title != "Editor" || capture.Mode != "visible-screen-crop" || capture.GeometrySource != "window-manager-reported" {
		t.Fatalf("capture metadata=%+v", capture)
	}
	if capture.Occlusion != "desktop composite; overlapping windows may appear" {
		t.Fatalf("occlusion metadata=%q", capture.Occlusion)
	}
	if grabs != 1 {
		t.Fatalf("screen captures=%d, want exactly one", grabs)
	}
	if len(manager.actions) != 0 {
		t.Fatalf("window actions=%v, capture must not activate or mutate the window", manager.actions)
	}
}

func TestCaptureWindowAcceptsUniformHiDPIScale(t *testing.T) {
	info := window.Info{NativeID: "scaled", Title: "Scaled", X: 5, Y: 10, W: 40, H: 30}
	session, _, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, rect.Dx()*2, rect.Dy()*2)), nil
	})
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Scaled"})
	if err != nil {
		t.Fatal(err)
	}

	capture, err := session.Screen.CaptureWindow(context.Background(), target)
	if err != nil {
		t.Fatalf("CaptureWindow: %v", err)
	}
	if capture.PixelScaleX != 2 || capture.PixelScaleY != 2 {
		t.Fatalf("pixel scale=(%v,%v), want (2,2)", capture.PixelScaleX, capture.PixelScaleY)
	}
	point, err := capture.Capture.ScreenPoint(image.Pt(2, 2))
	if err != nil || point != image.Pt(6, 11) {
		t.Fatalf("ScreenPoint=(%v,%v), want (6,11)", point, err)
	}
}

func TestCaptureWindowRejectsClippedCapture(t *testing.T) {
	info := window.Info{NativeID: "offscreen", Title: "Offscreen", X: -10, Y: 10, W: 30, H: 20}
	grabs := 0
	session, _, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		grabs++
		return image.NewRGBA(image.Rect(0, 0, rect.Dx()-10, rect.Dy())), nil
	})
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Offscreen"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := session.Screen.CaptureWindow(context.Background(), target); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("CaptureWindow error=%v, want ErrUnsupported for clipped geometry", err)
	}
	if grabs != 1 {
		t.Fatalf("screen captures=%d, want one attempted capture", grabs)
	}
}

func TestCaptureWindowRejectsMinimizedAndMissingGeometryBeforeCapture(t *testing.T) {
	for _, tc := range []struct {
		name string
		info window.Info
	}{
		{name: "minimized", info: window.Info{NativeID: "min", Title: "Minimized", X: 1, Y: 2, W: 20, H: 10, Minimized: true}},
		{name: "missing geometry", info: window.Info{NativeID: "unknown", Title: "Unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			grabs := 0
			session, _, _ := windowCaptureFixture(t, tc.info, func(context.Context, image.Rectangle) (image.Image, error) {
				grabs++
				return image.NewRGBA(image.Rect(0, 0, 32, 32)), nil
			})
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: tc.info.Title})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Screen.CaptureWindow(context.Background(), target); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("CaptureWindow error=%v, want ErrUnsupported", err)
			}
			if grabs != 0 {
				t.Fatalf("screen captures=%d, want zero", grabs)
			}
		})
	}
}

func TestCaptureWindowRejectsIdentityOrGeometryChangesDuringCapture(t *testing.T) {
	for _, tc := range []struct {
		name    string
		updated window.Info
		wantErr error
	}{
		{name: "identity", updated: window.Info{NativeID: "same-id", Title: "Replacement", AppID: "editor", PID: 41, X: 1, Y: 2, W: 20, H: 10}, wantErr: ErrWindowIdentityChanged},
		{name: "geometry", updated: window.Info{NativeID: "same-id", Title: "Editor", AppID: "editor", PID: 41, X: 3, Y: 2, W: 20, H: 10}, wantErr: ErrWindowGeometryChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := window.Info{NativeID: "same-id", Title: "Editor", AppID: "editor", PID: 41, X: 1, Y: 2, W: 20, H: 10}
			var manager *handleWindowManager
			session, m, _ := windowCaptureFixture(t, initial, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
				manager.setWindows(false, tc.updated)
				return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
			})
			manager = m
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.Screen.CaptureWindow(context.Background(), target); !errors.Is(err, tc.wantErr) {
				t.Fatalf("CaptureWindow error=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCaptureWindowRejectsReplacementBeforeCapture(t *testing.T) {
	initial := window.Info{NativeID: "reused-id", Title: "Editor", AppID: "editor", PID: 41, X: 1, Y: 2, W: 20, H: 10}
	grabs := 0
	session, manager, _ := windowCaptureFixture(t, initial, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		grabs++
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}
	manager.setWindows(false, window.Info{NativeID: "reused-id", Title: "Replacement", AppID: "other", PID: 99, X: 1, Y: 2, W: 20, H: 10})
	if _, err := session.Screen.CaptureWindow(context.Background(), target); !errors.Is(err, ErrWindowIdentityChanged) {
		t.Fatalf("CaptureWindow error=%v, want ErrWindowIdentityChanged", err)
	}
	if grabs != 0 {
		t.Fatalf("screen captures=%d, want zero after identity replacement", grabs)
	}
}

func TestCaptureWindowRejectsCrossSessionHandleBeforeCapture(t *testing.T) {
	info := window.Info{NativeID: "window", Title: "Editor", X: 1, Y: 2, W: 20, H: 10}
	first, _, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	target, err := first.Windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}
	grabs := 0
	second, _, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		grabs++
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	if _, err := second.Screen.CaptureWindow(context.Background(), target); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CaptureWindow error=%v, want ErrInvalidArgument", err)
	}
	if grabs != 0 {
		t.Fatalf("screen captures=%d, want zero", grabs)
	}
}

func TestCaptureWindowToWritesPNGAndReturnsMetadata(t *testing.T) {
	info := window.Info{NativeID: "editor", Title: "Editor", X: 3, Y: 4, W: 5, H: 2}
	session, _, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "visible.png")
	capture, err := session.Screen.CaptureWindowTo(context.Background(), target, path)
	if err != nil {
		t.Fatalf("CaptureWindowTo: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output PNG: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode output PNG: %v", err)
	}
	if decoded.Bounds().Dx() != info.W || decoded.Bounds().Dy() != info.H || capture.WindowID != "editor" {
		t.Fatalf("PNG bounds=%v capture=%+v", decoded.Bounds(), capture)
	}
}

func TestCaptureWindowToRejectsEmptyPathBeforeCaptureAndPropagatesFailures(t *testing.T) {
	captureErr := errors.New("capture failed")
	for _, tc := range []struct {
		name      string
		path      func(*testing.T) string
		grabErr   error
		wantErr   error
		wantGrabs int
	}{
		{name: "empty path", path: func(*testing.T) string { return "" }, wantErr: ErrInvalidArgument},
		{name: "capture failure", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "capture.png") }, grabErr: captureErr, wantErr: captureErr, wantGrabs: 1},
		{name: "publish failure", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing", "capture.png") }, wantGrabs: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := window.Info{NativeID: "editor", Title: "Editor", X: 1, Y: 2, W: 3, H: 2}
			grabs := 0
			session, _, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
				grabs++
				if tc.grabErr != nil {
					return nil, tc.grabErr
				}
				return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
			})
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.Screen.CaptureWindowTo(context.Background(), target, tc.path(t))
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("CaptureWindowTo error=%v, want error matching %v", err, tc.wantErr)
			}
			if grabs != tc.wantGrabs {
				t.Fatalf("screen captures=%d, want %d", grabs, tc.wantGrabs)
			}
		})
	}
}
