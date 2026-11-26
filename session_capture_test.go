package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
)

type cancelDuringPNGEncoding struct {
	cancel context.CancelFunc
	once   sync.Once
}

func (i *cancelDuringPNGEncoding) ColorModel() color.Model { return color.RGBAModel }
func (i *cancelDuringPNGEncoding) Bounds() image.Rectangle { return image.Rect(0, 0, 8, 8) }
func (i *cancelDuringPNGEncoding) At(x, y int) color.Color {
	i.once.Do(i.cancel)
	return color.RGBA{R: 0x12, G: 0x23, B: 0x34, A: 0xff}
}

// A screenshot may be valid when captured, but its PNG encoder can outlive
// caller authorization. Never publish an encoded file after cancellation.
func TestCaptureRegionDoesNotPublishAfterEncodingCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img := &cancelDuringPNGEncoding{cancel: cancel}
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	dir := t.TempDir()
	path := filepath.Join(dir, "screen.png")
	const original = "keep previous screenshot"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	err := session.screen.CaptureRegion(ctx, img.Bounds(), path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("encoder cancellation acknowledged as success: %v", err)
	}
	saved, readErr := os.ReadFile(path)
	if readErr != nil || string(saved) != original {
		t.Fatalf("canceled encoding replaced old screenshot: %q %v", saved, readErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "screen.png" {
		t.Fatalf("canceled encoding left staging file: %v %v", entries, readErr)
	}
}

type closeSessionDuringPNGEncoding struct {
	onRead func()
	once   sync.Once
}

func (*closeSessionDuringPNGEncoding) ColorModel() color.Model { return color.RGBAModel }
func (*closeSessionDuringPNGEncoding) Bounds() image.Rectangle { return image.Rect(0, 0, 8, 8) }
func (i *closeSessionDuringPNGEncoding) At(int, int) color.Color {
	i.once.Do(i.onRead)
	return color.RGBA{R: 0x12, G: 0x23, B: 0x34, A: 0xff}
}

// The PNG writer can continue pulling pixels after the session has been
// closed. A successfully encoded image from that revoked session must not
// replace the existing destination or leave a temporary file behind.
func TestCaptureRegionDoesNotPublishAfterSessionClosesDuringEncoding(t *testing.T) {
	img := &closeSessionDuringPNGEncoding{}
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	img.onRead = func() {
		if err := session.Close(); err != nil {
			t.Errorf("close session: %v", err)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "screen.png")
	const original = "retain previous valid screenshot"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	err := session.screen.CaptureRegion(context.Background(), img.Bounds(), path)
	if !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("revoked encoding incorrectly published: %v", err)
	}
	saved, readErr := os.ReadFile(path)
	if readErr != nil || string(saved) != original {
		t.Fatalf("revoked capture replaced prior image: %q %v", saved, readErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "screen.png" {
		t.Fatalf("revoked capture retained temporary files: %v %v", entries, readErr)
	}
}

type syntheticMalformedScreenshotter struct{ frame image.Image }

func (s syntheticMalformedScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return s.frame, nil
}
func (s syntheticMalformedScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (s syntheticMalformedScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (s syntheticMalformedScreenshotter) Close() error { return nil }

func TestScreenBundleNeverPublishesMalformedCaptureOrOverwritesExistingPNG(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"nil image", nil},
		{"typed nil image", (*image.RGBA)(nil)},
		{"RGBA truncated", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"NRGBA truncated", &image.NRGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"Gray truncated", &image.Gray{Rect: image.Rect(0, 0, 2, 2), Stride: 2, Pix: make([]byte, 3)}},
		{"Paletted truncated", &image.Paletted{Rect: image.Rect(0, 0, 2, 2), Stride: 2, Pix: make([]byte, 3)}},
		{"RGBA64 truncated", &image.RGBA64{Rect: image.Rect(0, 0, 2, 2), Stride: 16, Pix: make([]byte, 25)}},
		{"NRGBA64 truncated", &image.NRGBA64{Rect: image.Rect(0, 0, 2, 2), Stride: 16, Pix: make([]byte, 31)}},
		{"Gray16 truncated", &image.Gray16{Rect: image.Rect(0, 0, 2, 2), Stride: 4, Pix: make([]byte, 7)}},
		{"Alpha truncated", &image.Alpha{Rect: image.Rect(0, 0, 2, 2), Stride: 2, Pix: make([]byte, 3)}},
		{"Alpha16 truncated", &image.Alpha16{Rect: image.Rect(0, 0, 2, 2), Stride: 4, Pix: make([]byte, 7)}},
		{"CMYK truncated", &image.CMYK{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 15)}},
		{"inverted bounds", &image.RGBA{Rect: image.Rectangle{Min: image.Pt(2, 0), Max: image.Pt(1, 2)}, Stride: 8, Pix: make([]byte, 16)}},
		{"width overflow", &image.RGBA{Rect: image.Rect(0, 0, maxInt, 1), Stride: maxInt}},
		{"YCbCr Y plane truncated", &image.YCbCr{
			Rect: image.Rect(0, 0, 2, 2), YStride: 2, CStride: 1,
			Y: make([]byte, 3), Cb: make([]byte, 1), Cr: make([]byte, 1),
			SubsampleRatio: image.YCbCrSubsampleRatio420,
		}},
		{"YCbCr Cb plane truncated", &image.YCbCr{
			Rect: image.Rect(0, 0, 2, 2), YStride: 2, CStride: 1,
			Y: make([]byte, 4), Cr: make([]byte, 1),
			SubsampleRatio: image.YCbCrSubsampleRatio420,
		}},
		{"YCbCr Cr plane truncated", &image.YCbCr{
			Rect: image.Rect(0, 0, 2, 2), YStride: 2, CStride: 1,
			Y: make([]byte, 4), Cb: make([]byte, 1),
			SubsampleRatio: image.YCbCrSubsampleRatio420,
		}},
		{"NYCbCrA alpha plane truncated", &image.NYCbCrA{
			YCbCr: image.YCbCr{
				Rect: image.Rect(0, 0, 2, 2), YStride: 2, CStride: 1,
				Y: make([]byte, 4), Cb: make([]byte, 1), Cr: make([]byte, 1),
				SubsampleRatio: image.YCbCrSubsampleRatio420,
			},
			A: make([]byte, 3), AStride: 2,
		}},
		{"NYCbCrA chroma plane truncated", &image.NYCbCrA{
			YCbCr: image.YCbCr{
				Rect: image.Rect(0, 0, 2, 2), YStride: 2, CStride: 1,
				Y: make([]byte, 4), Cr: make([]byte, 1),
				SubsampleRatio: image.YCbCrSubsampleRatio420,
			},
			A: make([]byte, 4), AStride: 2,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newSessionForTesting(syntheticMalformedScreenshotter{frame: tc.img}, nil, nil, nil, nil)
			defer session.Close()
			ctx := context.Background()
			if _, err := session.screen.Grab(ctx, image.Rect(0, 0, 2, 2)); err == nil || !strings.Contains(err.Error(), "invalid capture") {
				t.Fatalf("malformed provider image exposed: %v", err)
			}
			target := filepath.Join(t.TempDir(), "existing.png")
			if err := os.WriteFile(target, []byte("preserve original"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := session.screen.CaptureRegion(ctx, image.Rect(0, 0, 2, 2), target); err == nil || !strings.Contains(err.Error(), "invalid capture") {
				t.Fatalf("malformed provider capture published: %v", err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "preserve original" {
				t.Fatalf("malformed capture overwrote existing file: %v %q", err, data)
			}
			matches, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".perfuncted-capture-*"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("temporary capture leaked: %v %v", matches, err)
			}
		})
	}
}

func TestScreenBundleAcceptsZeroWidthYCbCrBoundsWithoutPixelPlanes(t *testing.T) {
	frame := &image.YCbCr{Rect: image.Rect(4, 4, 4, 6), SubsampleRatio: image.YCbCrSubsampleRatio420}
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	defer session.Close()
	got, err := session.screen.Grab(context.Background(), frame.Bounds())
	if err != nil || got != frame {
		t.Fatalf("valid zero-width YCbCr capture rejected or rewritten: %v %T", err, got)
	}
}

func TestScreenBundlePreservesValidOddOriginYCbCrSubimage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ratio image.YCbCrSubsampleRatio
	}{
		{"444", image.YCbCrSubsampleRatio444},
		{"422", image.YCbCrSubsampleRatio422},
		{"420", image.YCbCrSubsampleRatio420},
		{"440", image.YCbCrSubsampleRatio440},
		{"411", image.YCbCrSubsampleRatio411},
		{"410", image.YCbCrSubsampleRatio410},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := image.NewYCbCr(image.Rect(10, 20, 15, 25), tc.ratio)
			for i := range parent.Y {
				parent.Y[i] = byte(60 + i)
			}
			for i := range parent.Cb {
				parent.Cb[i] = byte(110 + i)
				parent.Cr[i] = byte(140 + i)
			}
			frame := parent.SubImage(image.Rect(11, 21, 15, 25))
			session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
			defer session.Close()
			ctx := context.Background()
			if got, err := session.screen.Grab(ctx, frame.Bounds()); err != nil || got != frame {
				t.Fatalf("valid odd-origin YCbCr subimage rejected or rewritten: %v %T", err, got)
			}
			expected := color.RGBAModel.Convert(frame.At(14, 24)).(color.RGBA) //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
			target := filepath.Join(t.TempDir(), "capture.png")
			if err := session.screen.CaptureRegion(ctx, frame.Bounds(), target); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			decoded, err := png.Decode(file)
			if err != nil {
				t.Fatalf("valid YCbCr PNG capture undecodable: %v", err)
			}
			if got := color.RGBAModel.Convert(decoded.At(3, 3)).(color.RGBA); got != expected { //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
				t.Fatalf("odd-origin YCbCr pixel changed: got %+v want %+v", got, expected)
			}
		})
	}
}

// A subimage's Pix ends after its final visible row. It need not contain
// trailing stride padding, and its nonzero bounds are legitimate metadata.
func TestScreenBundlePreservesValidPaddedSubimageAndEncodesExpectedPixels(t *testing.T) {
	frame := &image.RGBA{Rect: image.Rect(10, 20, 12, 22), Stride: 12, Pix: []byte{
		1, 2, 3, 255, 5, 6, 7, 255, 0xc1, 0xc2, 0xc3, 0xc4,
		9, 10, 11, 255, 13, 14, 15, 255,
	}}
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	defer session.Close()
	ctx := context.Background()
	got, err := session.screen.Grab(ctx, image.Rect(10, 20, 12, 22))
	if err != nil || got != frame {
		t.Fatalf("valid subimage rejected or rewritten: %v %T", err, got)
	}
	target := filepath.Join(t.TempDir(), "capture.png")
	if err := session.screen.CaptureRegion(ctx, image.Rect(10, 20, 12, 22), target); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		t.Fatalf("valid PNG capture undecodable: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Fatalf("invalid encoded dimensions %v", b)
	}
	if got := color.RGBAModel.Convert(decoded.At(1, 1)).(color.RGBA); got != (color.RGBA{R: 13, G: 14, B: 15, A: 255}) { //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
		t.Fatalf("last visible pixel mutated: %+v", got)
	}
}

// bundleBase is embedded by value, so the promoted trace method's receiver is
// &s.bundleBase. Calling a traced entry point on a nil bundle dereferenced it to
// compute that receiver and panicked before any nil check could report the
// unavailable session.
func TestNilScreenBundleCaptureReturnsUnavailable(t *testing.T) {
	var s ScreenBundle
	if _, err := s.Capture(context.Background(), image.Rect(0, 0, 10, 10)); !errors.Is(err, ErrNilSession) {
		t.Fatalf("Capture on a nil bundle = %v, want ErrNilSession", err)
	}
}

// Every traced entry point has to tolerate a nil receiver, because each traces
// before it checks the backend.
func TestNilScreenBundleTracedEntrypointsDoNotPanic(t *testing.T) {
	var s ScreenBundle
	ctx := context.Background()
	rect := image.Rect(0, 0, 10, 10)

	_, _ = s.Grab(ctx, rect)
	_, _ = s.GetAllPixels(ctx)
	_ = s.CaptureRegion(ctx, rect, "")
	_, _ = s.GetPixel(ctx, 0, 0)
	_, _ = s.GetMultiplePixels(ctx, []image.Point{{}})
	_, _ = s.WaitForFn(ctx, rect, func(context.Context, image.Image) bool { return true }, time.Millisecond)
	_, _ = s.Capture(ctx, rect)
}

type captureTestScreenshotter struct {
	img       image.Image
	lastRect  image.Rectangle
	grabCalls int
}

func (s *captureTestScreenshotter) Grab(_ context.Context, rect image.Rectangle) (image.Image, error) {
	s.lastRect = rect
	s.grabCalls++
	return s.img, nil
}

func (*captureTestScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (*captureTestScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (*captureTestScreenshotter) Close() error { return nil }

var _ screen.Screenshotter = (*captureTestScreenshotter)(nil)

func TestScreenCapturePreservesRequestedScreenRect(t *testing.T) {
	backend := &captureTestScreenshotter{img: image.NewRGBA(image.Rect(20, 30, 24, 34))}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	wantRect := image.Rect(100, 200, 104, 204)
	capture, err := session.screen.Capture(context.Background(), wantRect)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if capture.Image != backend.img {
		t.Fatal("Capture returned a different image")
	}
	if capture.ScreenRect != wantRect {
		t.Fatalf("ScreenRect = %v, want %v", capture.ScreenRect, wantRect)
	}
	if backend.lastRect != wantRect || backend.grabCalls != 1 {
		t.Fatalf("backend received rect=%v calls=%d, want rect=%v calls=1", backend.lastRect, backend.grabCalls, wantRect)
	}
}

func TestScreenCaptureRejectsUnknownFullScreenProvenance(t *testing.T) {
	img := image.NewRGBA(image.Rect(3, 4, 8, 10))
	backend := &captureTestScreenshotter{img: img}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	capture, err := session.screen.Capture(context.Background(), image.Rectangle{})
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Capture error = %v, want ErrInvalidArgument", err)
	}
	if capture.Image != nil || !capture.ScreenRect.Empty() {
		t.Fatalf("Capture result = %#v, want empty capture", capture)
	}
	if backend.grabCalls != 0 {
		t.Fatalf("backend grab calls = %d, want 0", backend.grabCalls)
	}
}

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
	session := newSessionForTesting(backend, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := session.screen.CaptureWindowBackground(context.Background(), target)
	if err != nil {
		t.Fatalf("CaptureWindowBackground: %v", err)
	}
	if capture.Mode != screen.WindowCaptureBackgroundPixmap || capture.State != screen.WindowCaptureOccluded || capture.WindowID != info.NativeID {
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
		want       screen.WindowCaptureState
	}{
		{name: "known inactive", known: true, want: screen.WindowCaptureBackgroundVisible},
		{name: "known foreground", foreground: true, known: true, want: screen.WindowCaptureForeground},
		{name: "focus property unavailable", want: screen.WindowCaptureStateUnknown},
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
			session := newSessionForTesting(backend, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			target, err := session.windows.Find(context.Background(), window.Match{TitleExact: info.Title})
			if err != nil {
				t.Fatal(err)
			}
			capture, err := session.screen.CaptureWindowBackground(context.Background(), target)
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
			session := newSessionForTesting(backend, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			target, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Editor"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.screen.CaptureWindowBackground(context.Background(), target)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("CaptureWindowBackground error=%v, want ErrUnsupported", err)
			}
			if tc.name == "minimized" && !errors.Is(err, screen.ErrWindowCaptureMinimized) {
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
	session := newSessionForTesting(backend, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.screen.CaptureWindowBackground(context.Background(), target); !errors.Is(err, ErrUnsupported) || !errors.Is(err, screen.ErrWindowCaptureUnmapped) {
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
	session := newSessionForTesting(backend, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "background.png")
	capture, err := session.screen.CaptureWindowBackgroundTo(context.Background(), target, path)
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
	if decoded.Bounds().Dx() != info.W || decoded.Bounds().Dy() != info.H || capture.WindowID != "editor" || capture.Mode != screen.WindowCaptureBackgroundPixmap {
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
			session := newSessionForTesting(backend, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			target, err := session.windows.Find(context.Background(), window.Match{TitleExact: info.Title})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.screen.CaptureWindowBackgroundTo(context.Background(), target, tc.path(t))
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("CaptureWindowBackgroundTo error=%v, want error matching %v", err, tc.wantErr)
			}
			if captures != tc.wantCaptures {
				t.Fatalf("background captures=%d, want %d", captures, tc.wantCaptures)
			}
		})
	}
}

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
) (*sessionCore, *handleWindowManager) {
	t.Helper()
	manager := newHandleWindowManager(info)
	screenshot := &windowCaptureScreenshotter{grab: grab}
	session := newSessionForTesting(screenshot, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	return session, manager
}

func TestCaptureWindowUsesFreshBoundsAndDoesNotActivate(t *testing.T) {
	info := window.Info{
		NativeID: "editor-1", Title: "Editor", AppID: "org.example.Editor", PID: 41,
		X: -20, Y: 30, W: 40, H: 25,
	}
	want := image.Rect(-20, 30, 20, 55)
	var got image.Rectangle
	grabs := 0
	session, manager := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		got = rect
		grabs++
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}

	capture, err := session.screen.CaptureWindow(context.Background(), target)
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
	session, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, rect.Dx()*2, rect.Dy()*2)), nil
	})
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Scaled"})
	if err != nil {
		t.Fatal(err)
	}

	capture, err := session.screen.CaptureWindow(context.Background(), target)
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
	session, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		grabs++
		return image.NewRGBA(image.Rect(0, 0, rect.Dx()-10, rect.Dy())), nil
	})
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Offscreen"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := session.screen.CaptureWindow(context.Background(), target); !errors.Is(err, ErrUnsupported) {
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
			session, _ := windowCaptureFixture(t, tc.info, func(context.Context, image.Rectangle) (image.Image, error) {
				grabs++
				return image.NewRGBA(image.Rect(0, 0, 32, 32)), nil
			})
			target, err := session.windows.Find(context.Background(), window.Match{TitleExact: tc.info.Title})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.screen.CaptureWindow(context.Background(), target); !errors.Is(err, ErrUnsupported) {
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
			session, m := windowCaptureFixture(t, initial, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
				manager.setWindows(false, tc.updated)
				return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
			})
			manager = m
			target, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Editor"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.screen.CaptureWindow(context.Background(), target); !errors.Is(err, tc.wantErr) {
				t.Fatalf("CaptureWindow error=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCaptureWindowRejectsReplacementBeforeCapture(t *testing.T) {
	initial := window.Info{NativeID: "reused-id", Title: "Editor", AppID: "editor", PID: 41, X: 1, Y: 2, W: 20, H: 10}
	grabs := 0
	session, manager := windowCaptureFixture(t, initial, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		grabs++
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}
	manager.setWindows(false, window.Info{NativeID: "reused-id", Title: "Replacement", AppID: "other", PID: 99, X: 1, Y: 2, W: 20, H: 10})
	if _, err := session.screen.CaptureWindow(context.Background(), target); !errors.Is(err, ErrWindowIdentityChanged) {
		t.Fatalf("CaptureWindow error=%v, want ErrWindowIdentityChanged", err)
	}
	if grabs != 0 {
		t.Fatalf("screen captures=%d, want zero after identity replacement", grabs)
	}
}

func TestCaptureWindowRejectsCrossSessionHandleBeforeCapture(t *testing.T) {
	info := window.Info{NativeID: "window", Title: "Editor", X: 1, Y: 2, W: 20, H: 10}
	first, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	target, err := first.windows.Find(context.Background(), window.Match{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}
	grabs := 0
	second, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		grabs++
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	if _, err := second.screen.CaptureWindow(context.Background(), target); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("CaptureWindow error=%v, want ErrInvalidArgument", err)
	}
	if grabs != 0 {
		t.Fatalf("screen captures=%d, want zero", grabs)
	}
}

func TestCaptureWindowToWritesPNGAndReturnsMetadata(t *testing.T) {
	info := window.Info{NativeID: "editor", Title: "Editor", X: 3, Y: 4, W: 5, H: 2}
	session, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
	})
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "visible.png")
	capture, err := session.screen.CaptureWindowTo(context.Background(), target, path)
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
			session, _ := windowCaptureFixture(t, info, func(_ context.Context, rect image.Rectangle) (image.Image, error) {
				grabs++
				if tc.grabErr != nil {
					return nil, tc.grabErr
				}
				return image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy())), nil
			})
			target, err := session.windows.Find(context.Background(), window.Match{TitleExact: info.Title})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.screen.CaptureWindowTo(context.Background(), target, tc.path(t))
			if err == nil || (tc.wantErr != nil && !errors.Is(err, tc.wantErr)) {
				t.Fatalf("CaptureWindowTo error=%v, want error matching %v", err, tc.wantErr)
			}
			if grabs != tc.wantGrabs {
				t.Fatalf("screen captures=%d, want %d", grabs, tc.wantGrabs)
			}
		})
	}
}

type cancelDuringPixelInspection struct {
	after  func()
	called bool
}

func (i *cancelDuringPixelInspection) ColorModel() color.Model { return color.RGBAModel }
func (i *cancelDuringPixelInspection) Bounds() image.Rectangle { return image.Rect(0, 0, 2, 2) }
func (i *cancelDuringPixelInspection) At(x, y int) color.Color {
	if !i.called {
		i.called = true
		i.after()
	}
	return color.RGBA{R: 10, G: 20, B: 30, A: 255}
}

// A capture can succeed before the image provider begins exposing pixels.
// Cancellation or Session.Close during inspection must discard the entire
// partial result, not return colors read after authorization ended.
func TestGetMultiplePixelsRejectsAuthorityLossDuringPixelInspection(t *testing.T) {
	for _, mode := range []string{"cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			img := &cancelDuringPixelInspection{}
			session := newSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			if mode == "cancel" {
				img.after = cancel
			} else {
				img.after = func() { _ = session.Close() }
			}
			colors, err := session.screen.GetMultiplePixels(ctx, []image.Point{{X: 0, Y: 0}, {X: 1, Y: 1}})
			expected := context.Canceled
			if mode == "session-close" {
				expected = ErrSessionClosed
			}
			if !errors.Is(err, expected) || len(colors) != 0 || !img.called {
				t.Fatalf("pixel inspection with %s leaked colors=%v error=%v provider called=%v", mode, colors, err, img.called)
			}
		})
	}
}

type recoverableCaptureBackend struct {
	fail  error
	calls int
}

func (b *recoverableCaptureBackend) Grab(context.Context, image.Rectangle) (image.Image, error) {
	b.calls++
	if b.fail != nil {
		return nil, b.fail
	}
	return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
}
func (*recoverableCaptureBackend) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*recoverableCaptureBackend) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*recoverableCaptureBackend) Close() error { return nil }

// Backend failures and staging-file failures must fail closed without
// modifying an existing screenshot. The same session must remain usable once
// the transient backend failure has cleared.
func TestScreenBundleCaptureFailureDoesNotPublishOrPoisonRecovery(t *testing.T) {
	cause := errors.New("transient screen transport failure")
	backend := &recoverableCaptureBackend{fail: cause}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	defer session.Close()
	dir := t.TempDir()
	target := filepath.Join(dir, "screenshot.png")
	const old = "preserve the last authorized screenshot"
	if err := os.WriteFile(target, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	img, err := session.screen.Grab(context.Background(), image.Rectangle{})
	if img != nil || !errors.Is(err, cause) || !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("backend failure leaked image: image=%v err=%v", img, err)
	}
	err = session.screen.CaptureRegion(context.Background(), image.Rectangle{}, target)
	if !errors.Is(err, cause) || !errors.Is(err, ErrOperationFailed) {
		t.Fatalf("failed capture did not preserve backend cause: %v", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != old {
		t.Fatalf("failed capture replaced previous screenshot: %q %v", data, readErr)
	}
	backend.fail = nil
	inaccessible := filepath.Join(dir, "missing-parent", "capture.png")
	if err := session.screen.CaptureRegion(context.Background(), image.Rectangle{}, inaccessible); err == nil || !errors.Is(err, ErrOperationFailed) || !strings.Contains(err.Error(), "create") {
		t.Fatalf("missing staging directory did not fail closed: %v", err)
	}
	if _, err := os.Stat(inaccessible); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed staging unexpectedly published screenshot: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "screenshot.png" {
		t.Fatalf("failure left staging artifacts: %v %v", entries, err)
	}
	if err := session.screen.CaptureRegion(context.Background(), image.Rectangle{}, target); err != nil {
		t.Fatalf("capture did not recover: %v", err)
	}
	data, err = os.ReadFile(target)
	if err != nil || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("recovery did not publish valid PNG: %v", err)
	}
	if backend.calls != 4 {
		t.Fatalf("unexpected captures: %d, want four", backend.calls)
	}
}

type cancelAfterSuccessfulScreenRead struct {
	cancel context.CancelFunc
	calls  int
}

func (s *cancelAfterSuccessfulScreenRead) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	s.cancel()
	return image.NewRGBA(image.Rect(0, 0, 3, 2)), nil
}
func (*cancelAfterSuccessfulScreenRead) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*cancelAfterSuccessfulScreenRead) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*cancelAfterSuccessfulScreenRead) Close() error { return nil }

// Captures are not authorized simply because a backend returns a complete
// image. Cancellation during the capture callback must prevent every facade
// entrypoint from exposing those pixels or publishing a PNG.
func TestScreenBundleRejectsSuccessfulCaptureCanceledBeforeReturn(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*screenState, context.Context, string) (image.Image, error)
	}{
		{"grab", func(b *screenState, ctx context.Context, _ string) (image.Image, error) {
			return b.Grab(ctx, image.Rectangle{})
		}},
		{"region", func(b *screenState, ctx context.Context, _ string) (image.Image, error) {
			return b.GrabRegion(ctx, image.Rect(0, 0, 3, 2))
		}},
		{"all pixels", func(b *screenState, ctx context.Context, _ string) (image.Image, error) { return b.GetAllPixels(ctx) }},
		{"capture file", func(b *screenState, ctx context.Context, path string) (image.Image, error) {
			return nil, b.CaptureRegion(ctx, image.Rect(0, 0, 3, 2), path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &cancelAfterSuccessfulScreenRead{cancel: cancel}
			session := newSessionForTesting(source, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			path := filepath.Join(t.TempDir(), "screenshot.png")
			img, err := tc.run(session.screen, ctx, path)
			if img != nil || !errors.Is(err, context.Canceled) || source.calls != 1 {
				t.Fatalf("canceled backend returned image=%v error=%v calls=%d", img, err, source.calls)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("canceled capture published screenshot: %v", statErr)
			}
		})
	}
}

// A failed multi-pixel capture must never expose a partially initialized
// result, irrespective of how many private coordinates were requested.
func TestScreenMultiplePixelsRejectsCaptureTransportFailureWithoutPartialResults(t *testing.T) {
	failure := errors.New("synthetic screenshot transport unavailable")
	backend := &failingSettleBaselineCapture{failure: failure}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	points := []image.Point{{X: 1, Y: 2}, {X: 3, Y: 4}, {X: 5, Y: 6}}
	colors, err := session.screen.GetMultiplePixels(context.Background(), points)
	if colors != nil || !errors.Is(err, failure) || backend.calls != 1 {
		t.Fatalf("failed multi-pixel capture disclosed data: colors=%v err=%v backend calls=%d", colors, err, backend.calls)
	}
}

// The shared grab helper rejects nil (including typed-nil) images before
// returning. Multiple-pixel reads must preserve that rejection and never
// expose a partially initialized pixel slice.
func TestScreenMultiplePixelsRejectsNilCaptureThroughSharedValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{
		{name: "nil image", frame: nil},
		{name: "typed nil image", frame: (*image.RGBA)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newSessionForTesting(syntheticMalformedScreenshotter{frame: tc.frame}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			pixels, err := session.screen.GetMultiplePixels(context.Background(), []image.Point{{X: 3, Y: 4}, {X: 4, Y: 4}})
			if pixels != nil || err == nil || !strings.Contains(err.Error(), "nil image") {
				t.Fatalf("invalid capture exposed multiple pixels: pixels=%v err=%v", pixels, err)
			}
		})
	}
}

// ScreenBundle delegates nil caller contexts to backendContext, which treats
// them as Background. Post-capture checks must use that effective context too.
func TestScreenCaptureRegionNilContextDoesNotPanicAfterSuccessfulGrab(t *testing.T) {
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: image.NewRGBA(image.Rect(0, 0, 2, 2))}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	path := filepath.Join(t.TempDir(), "capture.png")
	defer func() {
		if failure := recover(); failure != nil {
			t.Errorf("CaptureRegion nil context panic: %v", failure)
		}
	}()
	if err := session.screen.CaptureRegion(nil, image.Rect(0, 0, 2, 2), path); err != nil {
		t.Fatalf("nil context capture: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	actual, err := png.Decode(f)
	if err != nil || actual.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("capture file bounds=%v err=%v", actual, err)
	}
}

func TestScreenMultiplePixelsNilContextDoesNotPanicAfterSuccessfulGrab(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, color.RGBA{R: 0x33, G: 0x44, B: 0x55, A: 0xff})
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	defer func() {
		if failure := recover(); failure != nil {
			t.Errorf("GetMultiplePixels nil context panic: %v", failure)
		}
	}()
	pixels, err := session.screen.GetMultiplePixels(nil, []image.Point{{X: 1, Y: 1}})
	if err != nil || len(pixels) != 1 || pixels[0] != (color.RGBA{R: 0x33, G: 0x44, B: 0x55, A: 0xff}) {
		t.Fatalf("nil context pixels=%v err=%v", pixels, err)
	}
}

func TestScreenBundleOperationsFailClosedWithoutBackend(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	ctx := context.Background()
	region := image.Rect(2, 3, 8, 9)
	cases := []struct {
		name      string
		operation string
		run       func() error
	}{
		{
			name:      "wait for no change",
			operation: "wait-stable",
			run: func() error {
				_, err := session.screen.WaitForNoChange(ctx, region, 1, time.Millisecond)
				return err
			},
		},
		{
			name:      "wait for no change from initial hash",
			operation: "wait-stable",
			run: func() error {
				_, err := session.screen.WaitForNoChangeFrom(ctx, region, 7, 1, time.Millisecond)
				return err
			},
		},
		{
			name:      "find color",
			operation: "pixel",
			run: func() error {
				_, err := session.screen.FindColor(ctx, region, color.RGBA{R: 1, A: 255}, 0)
				return err
			},
		},
		{
			name:      "wait for change",
			operation: "wait-change",
			run: func() error {
				_, err := session.screen.WaitForChange(ctx, region, 7, time.Millisecond)
				return err
			},
		},
		{
			name:      "wait for hash",
			operation: "wait",
			run: func() error {
				_, err := session.screen.WaitFor(ctx, region, 9, time.Millisecond)
				return err
			},
		},
		{
			name:      "scan for hash",
			operation: "wait",
			run: func() error {
				_, err := session.screen.ScanFor(ctx, []image.Rectangle{region}, []uint32{9}, time.Millisecond)
				return err
			},
		},
		{
			name:      "resolve dimensions",
			operation: "resolution",
			run: func() error {
				_, _, err := session.screen.Resolution(ctx)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			var unavailable *CapabilityError
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &unavailable) || unavailable.Operation != tc.operation {
				t.Fatalf("operation error = %v, want unavailable screen capability for %q", err, tc.operation)
			}
		})
	}
}

// A capture provider can complete with a valid frame just before the caller
// withdraws authority. The post-validation check must still reject pixels.
func TestScreenGrabRejectsCancellationAfterLayoutValidation(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	base, deadlineCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer deadlineCancel()
	owner, cancel := context.WithCancelCause(base)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 2}
	got, err := session.screen.Grab(ctx, frame.Bounds())
	if got != nil || !errors.Is(err, context.Canceled) || ctx.calls != 2 {
		t.Fatalf("completed frame published after cancellation: image=%v err=%v checks=%d", got, err, ctx.calls)
	}
}

// A caller can also cancel between a completed and validated Grab and the
// screenshot's filesystem staging. No transient file may be created then.
func TestScreenCaptureRegionRejectsCancellationBeforeFileStaging(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	base, deadlineCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer deadlineCancel()
	owner, cancel := context.WithCancelCause(base)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 3}
	root := t.TempDir()
	destination := filepath.Join(root, "private.png")
	err := session.screen.CaptureRegion(ctx, frame.Bounds(), destination)
	if !errors.Is(err, context.Canceled) || ctx.calls != 3 {
		t.Fatalf("canceled file staging: err=%v checks=%d", err, ctx.calls)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled screenshot left files: entries=%v err=%v", entries, err)
	}
}

// Settle must stop before running a caller action when the baseline capture
// fails, propagate an action failure without waiting for a change, and never
// return a stable hash when an action has not produced any visible change.
func TestScreenWaitForSettleRejectsFailedActionAndAbsentChange(t *testing.T) {
	rect := image.Rect(0, 0, 4, 4)
	actionErr := errors.New("synthetic action refused")
	t.Run("unavailable source never dispatches", func(t *testing.T) {
		session := newSessionForTesting(nil, nil, nil, nil, nil)
		defer session.Close()
		invoked := false
		hash, err := session.screen.WaitForSettle(context.Background(), rect, func() error { invoked = true; return nil }, 1, time.Millisecond)
		if hash != 0 || !errors.Is(err, ErrUnavailable) || invoked {
			t.Fatalf("unavailable screenshot source returned hash=%x err=%v invoked=%t", hash, err, invoked)
		}
	})
	t.Run("failed action does not wait or capture extra frames", func(t *testing.T) {
		frame := image.NewRGBA(rect)
		frame.SetRGBA(1, 1, color.RGBA{R: 255, A: 255})
		mock := &captureTestScreenshotter{img: frame}
		session := newSessionForTesting(mock, nil, nil, nil, nil)
		defer session.Close()
		calls := 0
		hash, err := session.screen.WaitForSettle(context.Background(), rect, func() error { calls++; return actionErr }, 2, time.Millisecond)
		if hash != 0 || !errors.Is(err, actionErr) || calls != 1 {
			t.Fatalf("failed action hash=%x err=%v calls=%d", hash, err, calls)
		}
	})
	t.Run("no visible change cannot be reported settled", func(t *testing.T) {
		frame := image.NewRGBA(rect)
		frame.SetRGBA(2, 2, color.RGBA{B: 255, A: 255})
		mock := &captureTestScreenshotter{img: frame}
		session := newSessionForTesting(mock, nil, nil, nil, nil)
		defer session.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		defer cancel()
		calls := 0
		hash, err := session.screen.WaitForSettle(ctx, rect, func() error { calls++; return nil }, 2, time.Millisecond)
		if hash != 0 || err == nil || calls != 1 {
			t.Fatalf("unchanged screen declared stable: hash=%x err=%v actionCalls=%d", hash, err, calls)
		}
	})
}

// The action may withdraw caller authority after the initial hash was read.
// No change-detection or stability polling is allowed after that cancellation.
func TestScreenWaitForSettleCancellingActionSkipsChangePoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &countingSettleCapture{syntheticMalformedScreenshotter: syntheticMalformedScreenshotter{frame: image.NewRGBA(image.Rect(0, 0, 2, 2))}}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	actions := 0
	hash, err := session.screen.WaitForSettle(ctx, image.Rect(0, 0, 2, 2), func() error {
		actions++
		cancel()
		return nil
	}, 1, time.Millisecond)
	if !errors.Is(err, context.Canceled) || hash != 0 || actions != 1 {
		t.Fatalf("canceled action proceeded: hash=%08x err=%v actions=%d", hash, err, actions)
	}
	if backend.grabs != 1 {
		t.Fatalf("canceled action started new screen polling phase: calls=%d", backend.grabs)
	}
}

// Window motion may be observed successfully just as the caller cancels.
// Before entering the stable-image phase the facade must check authority
// again, discarding the change hash without dispatching another screenshot.
func TestScreenSettleRejectsCallerRevocationAtPhaseBoundary(t *testing.T) {
	base, deadlineCancel := context.WithTimeout(context.Background(), time.Second)
	defer deadlineCancel()
	owner, cancel := context.WithCancelCause(base)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 10}
	source := &settleFinalCaptureRevocation{}
	session := newSessionForTesting(source, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	hash, err := session.screen.WaitForSettle(ctx, image.Rect(0, 0, 3, 3), nil, 1, time.Millisecond)
	if hash != 0 || !errors.Is(err, context.Canceled) || source.calls != 2 || ctx.calls != 10 {
		t.Fatalf("canceled phase transition: hash=%08x err=%v checks=%d captures=%d", hash, err, ctx.calls, source.calls)
	}
}

// The FirstPixel helper checks caller authority while acquiring and sampling
// the frame. The facade must independently check it once more before it
// releases the sampled private pixel to an application.
func TestScreenGetPixelFinalCallerRevocationDoesNotPublishSample(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, color.RGBA{R: 101, G: 44, B: 9, A: 255})
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	parent, cancelDeadline := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelDeadline()
	owner, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 4}
	pixel, err := session.screen.GetPixel(ctx, 0, 0)
	if pixel != (color.RGBA{}) || !errors.Is(err, context.Canceled) || ctx.calls != 4 {
		t.Fatalf("final caller revocation leaked pixel=%+v err=%v checks=%d", pixel, err, ctx.calls)
	}
}

// The screen facade must withhold a matching frame if its predicate revokes
// caller authorization, and it must preserve the predicate's own timeout
// context rather than treating a stale matching frame as success.
func TestScreenWaitForFnRejectsFrameAfterPredicateCancelsCaller(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 3, 2))
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	invoked := 0
	img, err := session.screen.WaitForFn(ctx, frame.Bounds(), func(_ context.Context, _ image.Image) bool {
		invoked++
		cancel()
		return true
	}, time.Millisecond)
	if invoked != 1 || img != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled predicate published image=%T err=%v calls=%d", img, err, invoked)
	}
}

// Malformed caller predicates must not silently report successful screenshots
// or spin until the deadline; the error is a caller input error, not a capture.
func TestScreenWaitForFnRejectsMissingPredicateBeforePolling(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 3, 2))
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	img, err := session.screen.WaitForFn(context.Background(), frame.Bounds(), nil, time.Millisecond)
	if img != nil || err == nil || !strings.Contains(err.Error(), "nil predicate") {
		t.Fatalf("missing predicate accepted: image=%T err=%v", img, err)
	}
}

// The predicate may complete after its session was closed. The desktop
// facade must not expose a captured image from that now-closed session.
func TestScreenWaitForFnDoesNotReturnImageAfterSessionClose(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	invoked := false
	got, err := session.screen.WaitForFn(context.Background(), img.Bounds(), func(_ context.Context, _ image.Image) bool {
		invoked = true
		if closeErr := session.Close(); closeErr != nil {
			t.Errorf("session close: %v", closeErr)
		}
		return true
	}, time.Millisecond)
	if !invoked || !errors.Is(err, ErrSessionClosed) || got != nil {
		t.Fatalf("wait after session close leaked %T, error=%v predicate invoked=%v", got, err, invoked)
	}
}

type closeDuringHashReadImage struct {
	onRead func()
	reads  int
}

func (*closeDuringHashReadImage) Bounds() image.Rectangle { return image.Rect(0, 0, 2, 2) }
func (*closeDuringHashReadImage) ColorModel() color.Model { return color.RGBAModel }
func (i *closeDuringHashReadImage) At(int, int) color.Color {
	i.reads++
	if i.reads == 1 {
		i.onRead()
	}
	return color.RGBA{R: 25, G: 34, B: 55, A: 255}
}

// A backend may return a lazy image whose hash computation runs after an
// owner closure. Neither full-screen nor region hashes can be published once
// the owner revokes access, even if the hash implementation completes.
func TestScreenHashesDoNotPublishAfterClosureDuringPixelHash(t *testing.T) {
	for _, tc := range []struct {
		name string
		hash func(*screenState) (uint32, error)
	}{
		{"full", func(b *screenState) (uint32, error) { return b.GrabFullHash(context.Background()) }},
		{"region", func(b *screenState) (uint32, error) {
			return b.GrabRegionHash(context.Background(), image.Rect(0, 0, 2, 2))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := &closeDuringHashReadImage{}
			session := newSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			img.onRead = func() {
				if err := session.Close(); err != nil {
					t.Errorf("close session: %v", err)
				}
			}
			value, err := tc.hash(session.screen)
			if value != 0 || !errors.Is(err, ErrSessionClosed) || img.reads == 0 {
				t.Fatalf("closed session published hash %08x, error=%v, pixel reads=%d", value, err, img.reads)
			}
		})
	}
}

type revokeOnLastPixelImage struct {
	reads      int
	onLastRead func()
}

func (*revokeOnLastPixelImage) Bounds() image.Rectangle { return image.Rect(0, 0, 2, 1) }
func (*revokeOnLastPixelImage) ColorModel() color.Model { return color.RGBAModel }
func (i *revokeOnLastPixelImage) At(int, int) color.Color {
	i.reads++
	if i.reads == 2 && i.onLastRead != nil {
		i.onLastRead()
	}
	return color.RGBA{R: 99, G: 42, B: 18, A: 255}
}

// The final pixel is still untrusted input. Cancellation or Session.Close
// during its At call must not publish an otherwise-complete private result.
// The post-loop check matters because there is no next iteration to enforce
// the per-pixel admission guard.
func TestScreenMultiplePixelsRejectsRevocationDuringFinalPixelRead(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cancelCaller bool
		want         error
	}{
		{name: "caller cancellation", cancelCaller: true, want: context.Canceled},
		{name: "session closure", want: ErrSessionClosed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			frame := &revokeOnLastPixelImage{}
			session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			frame.onLastRead = func() {
				if tc.cancelCaller {
					cancel()
					return
				}
				if err := session.Close(); err != nil {
					t.Errorf("session close: %v", err)
				}
			}
			pixels, err := session.screen.GetMultiplePixels(ctx, []image.Point{{0, 0}, {1, 0}})
			if pixels != nil || !errors.Is(err, tc.want) || frame.reads != 2 {
				t.Fatalf("revoked last read published data: pixels=%v err=%v reads=%d", pixels, err, frame.reads)
			}
		})
	}
}

type cancelDuringPixelRead struct {
	bounds image.Rectangle
	at     func()
	calls  int
}

func (i *cancelDuringPixelRead) Bounds() image.Rectangle { return i.bounds }
func (i *cancelDuringPixelRead) ColorModel() color.Model { return color.RGBAModel }
func (i *cancelDuringPixelRead) At(int, int) color.Color {
	i.calls++
	if i.calls == 1 && i.at != nil {
		i.at()
	}
	return color.RGBA{R: 99, G: 42, B: 18, A: 255}
}

// An untrusted backend can return a lazy image whose pixels are not retrieved
// until callers ask for them. Cancellation during the first read must prohibit
// subsequent reads and keep the partially read image out of the result.
func TestScreenMultiplePixelsStopsReadingAfterCancellationWithinImage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frame := &cancelDuringPixelRead{bounds: image.Rect(0, 0, 2, 1), at: cancel}
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	pixels, err := session.screen.GetMultiplePixels(ctx, []image.Point{{0, 0}, {1, 0}})
	if !errors.Is(err, context.Canceled) || pixels != nil || frame.calls != 1 {
		t.Fatalf("canceled pixel read disclosed result: pixels=%v err=%v calls=%d", pixels, err, frame.calls)
	}
}

// Session.Close after image capture withdraws the ability to expose pixels
// from that image, even when the original request context remains active.
func TestScreenMultiplePixelsStopsReadingAfterSessionCloseWithinImage(t *testing.T) {
	frame := &cancelDuringPixelRead{bounds: image.Rect(0, 0, 2, 1)}
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	frame.at = func() {
		if err := session.Close(); err != nil {
			t.Errorf("session close: %v", err)
		}
	}
	pixels, err := session.screen.GetMultiplePixels(context.Background(), []image.Point{{0, 0}, {1, 0}})
	if !errors.Is(err, ErrSessionClosed) || pixels != nil || frame.calls != 1 {
		t.Fatalf("closed session disclosed pixel data: pixels=%v err=%v calls=%d", pixels, err, frame.calls)
	}
}

type failingSettleBaselineCapture struct {
	failure error
	calls   int
}

func (s *failingSettleBaselineCapture) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	return nil, s.failure
}
func (*failingSettleBaselineCapture) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*failingSettleBaselineCapture) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*failingSettleBaselineCapture) Close() error { return nil }

// A failed initial frame is not authorization to act. Settle must return the
// original transport error without invoking the action or later polling.
func TestScreenWaitForSettleRejectsInitialCaptureTransportFailure(t *testing.T) {
	failure := errors.New("synthetic baseline capture transport failure")
	source := &failingSettleBaselineCapture{failure: failure}
	session := newSessionForTesting(source, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	actions := 0
	hash, err := session.screen.WaitForSettle(context.Background(), image.Rect(0, 0, 3, 2), func() error { actions++; return nil }, 1, time.Millisecond)
	if hash != 0 || !errors.Is(err, failure) || actions != 0 || source.calls != 1 {
		t.Fatalf("failed baseline permitted action: hash=%08x err=%v actions=%d captures=%d", hash, err, actions, source.calls)
	}
}

type cancelOnChangedFrame struct {
	cancel context.CancelFunc
	calls  int
}

func (s *cancelOnChangedFrame) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	frame := image.NewRGBA(image.Rect(0, 0, 3, 3))
	if s.calls >= 2 {
		frame.SetRGBA(0, 0, color.RGBA{R: 200, A: 255})
		if s.calls == 2 {
			s.cancel()
		}
	}
	return frame, nil
}
func (s *cancelOnChangedFrame) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (s *cancelOnChangedFrame) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (s *cancelOnChangedFrame) Close() error { return nil }

// A backend may observe a changed frame and cancel its caller before returning.
// Change detection must not authorize the next polling phase after cancellation,
// even when the changed hash itself was computed successfully.
func TestScreenSettleChangePhaseStopsOnCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancelOnChangedFrame{cancel: cancel}
	session := newSessionForTesting(source, nil, nil, nil, nil)
	defer session.Close()
	hash, err := session.screen.WaitForSettle(ctx, image.Rect(0, 0, 3, 3), nil, 1, time.Millisecond)
	if !errors.Is(err, context.Canceled) || hash != 0 || source.calls != 2 {
		t.Fatalf("canceled settle invoked additional captures: hash=%x err=%v calls=%d", hash, err, source.calls)
	}
}

type closingOnChangedFrame struct {
	owner *sessionCore
	calls int
}

func (s *closingOnChangedFrame) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	frame := image.NewRGBA(image.Rect(0, 0, 3, 3))
	if s.calls >= 2 {
		frame.SetRGBA(0, 0, color.RGBA{R: 200, A: 255})
		if s.calls == 2 {
			if err := s.owner.Close(); err != nil {
				return nil, err
			}
		}
	}
	return frame, nil
}
func (s *closingOnChangedFrame) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (s *closingOnChangedFrame) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (s *closingOnChangedFrame) Close() error { return nil }

// A session may close during the first change-detection frame. Once the
// changed hash returns, the facade must recheck ownership before starting the
// distinct settle phase, even if the caller context itself is still live.
func TestScreenSettleChangePhaseDoesNotPollAfterSessionCloses(t *testing.T) {
	source := &closingOnChangedFrame{}
	session := newSessionForTesting(source, nil, nil, nil, nil)
	source.owner = session
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	hash, err := session.screen.WaitForSettle(ctx, image.Rect(0, 0, 3, 3), nil, 1, time.Millisecond)
	if !errors.Is(err, ErrSessionClosed) || hash != 0 || source.calls != 2 {
		t.Fatalf("closed session entered second settle phase: hash=%x err=%v capture calls=%d", hash, err, source.calls)
	}
}

type settleFinalCaptureRevocation struct {
	calls              int
	afterStableCapture func()
}

func (f *settleFinalCaptureRevocation) Grab(_ context.Context, _ image.Rectangle) (image.Image, error) {
	f.calls++
	frame := image.NewRGBA(image.Rect(0, 0, 3, 3))
	if f.calls >= 2 {
		frame.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	}
	if f.calls == 3 && f.afterStableCapture != nil {
		f.afterStableCapture()
	}
	return frame, nil
}
func (*settleFinalCaptureRevocation) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*settleFinalCaptureRevocation) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*settleFinalCaptureRevocation) Close() error { return nil }

// A stable frame is not authoritative when either caller or owning Session
// is revoked before that capture returns; the wait must discard its hash.
func TestScreenWaitForSettleDiscardsFinalStableHashAfterRevocation(t *testing.T) {
	for _, mode := range []string{"caller canceled", "session closed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			frame := &settleFinalCaptureRevocation{}
			session := newSessionForTesting(frame, nil, nil, nil, nil)
			defer session.Close()
			want := context.Canceled
			if mode == "session closed" {
				want = ErrSessionClosed
				frame.afterStableCapture = func() { _ = session.Close() }
			} else {
				frame.afterStableCapture = cancel
			}
			hash, err := session.screen.WaitForSettle(ctx, image.Rect(0, 0, 3, 3), nil, 1, time.Millisecond)
			if !errors.Is(err, want) || hash != 0 || frame.calls != 3 {
				t.Fatalf("revoked stable frame acknowledged: hash=%x err=%v calls=%d want=%v", hash, err, frame.calls, want)
			}
		})
	}
}

type countingSettleCapture struct {
	syntheticMalformedScreenshotter
	grabs int
}

func (s *countingSettleCapture) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs++
	return s.syntheticMalformedScreenshotter.Grab(ctx, rect)
}

// A successful baseline does not authorize polling after an action closes the
// session. The action is an explicit user-controlled boundary in this facade.
func TestScreenSettleStopsWhenActionClosesSession(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 4, 4))
	source := &countingSettleCapture{syntheticMalformedScreenshotter: syntheticMalformedScreenshotter{frame: frame}}
	session := newSessionForTesting(source, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	actionCalls := 0
	hash, err := session.screen.WaitForSettle(ctx, frame.Bounds(), func() error {
		actionCalls++
		return session.Close()
	}, 1, time.Millisecond)
	if !errors.Is(err, ErrSessionClosed) || hash != 0 || actionCalls != 1 || source.grabs != 1 {
		t.Fatalf("closed session polled again: hash=%x err=%v actions=%d grabs=%d", hash, err, actionCalls, source.grabs)
	}
}

type closeDuringSinglePixelRead struct {
	after  func()
	called bool
}

func (i *closeDuringSinglePixelRead) ColorModel() color.Model { return color.RGBAModel }
func (i *closeDuringSinglePixelRead) Bounds() image.Rectangle { return image.Rect(0, 0, 2, 2) }
func (i *closeDuringSinglePixelRead) At(x, y int) color.Color {
	if !i.called {
		i.called = true
		i.after()
	}
	return color.RGBA{R: 10, G: 20, B: 30, A: 255}
}

// The image adapter may synchronously close the owning session while reading
// the requested pixel. A closed session cannot publish the sampled color.
func TestScreenGetPixelRejectsResultAfterSessionClosedDuringRead(t *testing.T) {
	img := &closeDuringSinglePixelRead{}
	session := newSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	img.after = func() { _ = session.Close() }
	pixel, err := session.screen.GetPixel(context.Background(), 0, 0)
	if !img.called || !errors.Is(err, ErrSessionClosed) || pixel != (color.RGBA{}) {
		t.Fatalf("closed session exposed pixel %+v err=%v img called=%v", pixel, err, img.called)
	}
}

type templateBundleScreenshotter struct {
	frame  image.Image
	err    error
	grabFn func(context.Context, image.Rectangle) (image.Image, error)
}

func (s *templateBundleScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if s.grabFn != nil {
		return s.grabFn(ctx, rect)
	}
	return s.frame, s.err
}

func (s *templateBundleScreenshotter) GrabVerified(ctx context.Context, rect image.Rectangle) (find.VerifiedTemplateCapture, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return find.VerifiedTemplateCapture{}, err
	}
	if img == nil || rect.Empty() || img.Bounds().Dx()%rect.Dx() != 0 || img.Bounds().Dy()%rect.Dy() != 0 {
		return find.VerifiedTemplateCapture{}, find.ErrUnverifiedCaptureGeometry
	}
	scaleX := img.Bounds().Dx() / rect.Dx()
	scaleY := img.Bounds().Dy() / rect.Dy()
	if scaleX <= 0 || scaleX != scaleY {
		return find.VerifiedTemplateCapture{}, find.ErrUnverifiedCaptureGeometry
	}
	return find.VerifiedTemplateCapture{Image: img, DesktopRect: rect, PixelScale: scaleX}, nil
}

func (*templateBundleScreenshotter) Close() error { return nil }
func (*templateBundleScreenshotter) GrabFullHash(context.Context) (uint32, error) {
	return 0, nil
}
func (*templateBundleScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func TestScreenBundleFindTemplateMapsHiDPIResultToDesktop(t *testing.T) {
	const scale = 2
	region := image.Rect(100, 200, 108, 208)
	frame := image.NewNRGBA(image.Rect(0, 0, region.Dx()*scale, region.Dy()*scale))
	template := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < template.Bounds().Dy(); y++ {
		for x := 0; x < template.Bounds().Dx(); x++ {
			pixel := color.NRGBA{R: uint8(30 + x*20), G: uint8(60 + y*20), B: uint8(90 + x + y), A: 255}
			template.SetNRGBA(x, y, pixel)
			frame.SetNRGBA(6+x, 8+y, pixel)
		}
	}
	backend := &templateBundleScreenshotter{frame: frame}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	got, err := session.screen.FindTemplate(context.Background(), region, template, 1)
	if err != nil {
		t.Fatalf("FindTemplate: %v", err)
	}
	want := image.Rect(103, 204, 105, 206)
	if got.Rect != want || got.Confidence != 1 {
		t.Fatalf("FindTemplate = %+v, want rect %v at confidence 1", got, want)
	}
}

func TestScreenBundleFindTemplatePreservesCaptureFailuresAndAvailability(t *testing.T) {
	region := image.Rect(0, 0, 4, 4)
	template := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, tc := range []struct {
		name    string
		backend *templateBundleScreenshotter
		wantErr error
	}{
		{name: "missing backend", wantErr: ErrUnavailable},
		{name: "capture error", backend: &templateBundleScreenshotter{err: os.ErrPermission}, wantErr: os.ErrPermission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newSessionForTesting(tc.backend, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			got, err := session.screen.FindTemplate(context.Background(), region, template, 0.95)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("FindTemplate error = %v, want %v", err, tc.wantErr)
			}
			if got != (find.TemplateMatch{}) {
				t.Fatalf("failed operation exposed a match: %+v", got)
			}
		})
	}
}

func TestScreenBundleFindTemplateRejectsInvalidThresholdBeforeCapture(t *testing.T) {
	calls := 0
	backend := &templateBundleScreenshotter{grabFn: func(context.Context, image.Rectangle) (image.Image, error) {
		calls++
		return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
	}}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	for _, threshold := range []float64{-0.01, 1.01} {
		if got, err := session.screen.FindTemplate(context.Background(), image.Rect(0, 0, 4, 4), image.NewRGBA(image.Rect(0, 0, 1, 1)), threshold); err == nil || got != (find.TemplateMatch{}) {
			t.Fatalf("invalid threshold %g returned %+v, %v", threshold, got, err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid thresholds reached backend %d times", calls)
	}
}

type gatedTemplateImage struct {
	bounds  image.Rectangle
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (g *gatedTemplateImage) ColorModel() color.Model { return color.NRGBAModel }
func (g *gatedTemplateImage) Bounds() image.Rectangle { return g.bounds }
func (g *gatedTemplateImage) At(_, _ int) color.Color {
	g.once.Do(func() {
		close(g.started)
		<-g.release
	})
	return color.NRGBA{R: 50, G: 80, B: 110, A: 255}
}

func TestScreenBundleFindTemplateReturnsNoResultAfterCloseOrTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		close   bool
		timeout bool
		wantErr error
	}{
		{name: "session close", close: true, wantErr: context.Canceled},
		{name: "operation timeout", timeout: true, wantErr: context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			operationContext := make(chan context.Context, 1)
			frame := &gatedTemplateImage{bounds: image.Rect(0, 0, 2, 2), started: started, release: release}
			backend := &templateBundleScreenshotter{grabFn: func(ctx context.Context, _ image.Rectangle) (image.Image, error) {
				operationContext <- ctx
				return frame, nil
			}}
			session := newSessionForTesting(backend, nil, nil, nil, nil)
			if tc.timeout {
				session.config.Timeouts = TimeoutPolicy{Medium: 20 * time.Millisecond}
			}
			result := make(chan struct {
				match find.TemplateMatch
				err   error
			}, 1)
			go func() {
				match, err := session.screen.FindTemplate(context.Background(), frame.bounds, image.NewNRGBA(image.Rect(0, 0, 1, 1)), 0.95)
				result <- struct {
					match find.TemplateMatch
					err   error
				}{match: match, err: err}
			}()
			<-started
			captureContext := <-operationContext
			if tc.close {
				if err := session.Close(); err != nil {
					t.Fatalf("close session: %v", err)
				}
			}
			<-captureContext.Done()
			close(release)
			got := <-result
			if !errors.Is(got.err, tc.wantErr) || got.match != (find.TemplateMatch{}) {
				t.Fatalf("FindTemplate after revocation = %+v, %v; want no match and %v", got.match, got.err, tc.wantErr)
			}
			_ = session.Close()
		})
	}
}

func TestScreenBundleFindTemplatesResultFailuresAndAuthority(t *testing.T) {
	area := image.Rect(0, 0, 3, 1)
	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	frame := image.NewNRGBA(area)
	frame.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	frame.SetNRGBA(2, 0, color.NRGBA{R: 255, A: 255})
	session := newSessionForTesting(&templateBundleScreenshotter{frame: frame}, nil, nil, nil, nil)
	got, err := session.screen.FindTemplates(context.Background(), area, ref, 1, 64)
	if err != nil || len(got) != 2 || got[0].Rect.Min.X != 0 || got[1].Rect.Min.X != 2 {
		t.Fatalf("matches=%v err=%v", got, err)
	}
	no, err := session.screen.FindTemplates(context.Background(), area, ref, 1, 0)
	if !errors.Is(err, find.ErrInvalidFrame) || no != nil {
		t.Fatalf("invalid=%v %v", no, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	no, err = session.screen.FindTemplates(ctx, area, ref, 1, 64)
	if !errors.Is(err, context.Canceled) || no != nil {
		t.Fatalf("cancelled=%v %v", no, err)
	}
	_ = session.Close()
	no, err = session.screen.FindTemplates(context.Background(), area, ref, 1, 64)
	if err == nil || no != nil {
		t.Fatalf("closed=%v %v", no, err)
	}
	missing := newSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = missing.Close() })
	no, err = missing.screen.FindTemplates(context.Background(), area, ref, 1, 64)
	if !errors.Is(err, ErrUnavailable) || no != nil {
		t.Fatalf("unavailable=%v %v", no, err)
	}
	denied := newSessionForTesting(&templateBundleScreenshotter{err: os.ErrPermission}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = denied.Close() })
	no, err = denied.screen.FindTemplates(context.Background(), area, ref, 1, 64)
	if !errors.Is(err, os.ErrPermission) || no != nil {
		t.Fatalf("denied=%v %v", no, err)
	}
}

type closingTemplateFrame struct {
	image.Image
	session *sessionCore
}

func (f *closingTemplateFrame) At(x, y int) color.Color {
	f.session.lifecycleMu.Lock()
	f.session.closed = true
	f.session.lifecycleMu.Unlock()
	return f.Image.At(x, y)
}
func TestScreenBundleFindTemplatesChecksLateClosedSession(t *testing.T) {
	region := image.Rect(0, 0, 1, 1)
	frame := image.NewNRGBA(region)
	frame.SetNRGBA(0, 0, color.NRGBA{A: 255, R: 255})
	backend := &templateBundleScreenshotter{}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	backend.frame = &closingTemplateFrame{Image: frame, session: session}
	reference := image.NewNRGBA(region)
	reference.SetNRGBA(0, 0, color.NRGBA{A: 255, R: 255})
	result, err := session.screen.FindTemplates(context.Background(), region, reference, 1, 1)
	session.lifecycleMu.Lock()
	session.closed = false // Restore synthetic state so Close runs its real cleanup.
	session.lifecycleMu.Unlock()
	t.Cleanup(func() { _ = session.Close() })
	if !errors.Is(err, ErrSessionClosed) || result != nil {
		t.Fatalf("late closure=%v %v", result, err)
	}
}

type textBundleScreenshotter struct {
	image  image.Image
	err    error
	calls  int
	region image.Rectangle
}

type plainTextScreenshotter struct{ image image.Image }

func (s *plainTextScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return s.image, nil
}
func (*plainTextScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*plainTextScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*plainTextScreenshotter) Close() error { return nil }

func (s *textBundleScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	return s.image, s.err
}
func (*textBundleScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*textBundleScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*textBundleScreenshotter) Close() error { return nil }
func (s *textBundleScreenshotter) GrabVerified(ctx context.Context, rect image.Rectangle) (find.VerifiedTemplateCapture, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return find.VerifiedTemplateCapture{}, err
	}
	if rect != s.region || img == nil || rect.Empty() || img.Bounds().Dx()%rect.Dx() != 0 || img.Bounds().Dy()%rect.Dy() != 0 {
		return find.VerifiedTemplateCapture{}, find.ErrUnverifiedCaptureGeometry
	}
	scaleX, scaleY := img.Bounds().Dx()/rect.Dx(), img.Bounds().Dy()/rect.Dy()
	if scaleX <= 0 || scaleX != scaleY {
		return find.VerifiedTemplateCapture{}, find.ErrUnverifiedCaptureGeometry
	}
	return find.VerifiedTemplateCapture{Image: img, DesktopRect: rect, PixelScale: scaleX}, nil
}

type bundleTextRecognizer struct {
	words []find.OCRWord
	err   error
	calls int
}

func (*bundleTextRecognizer) Name() string     { return "bundle-fixture" }
func (*bundleTextRecognizer) Language() string { return "eng" }
func (r *bundleTextRecognizer) Recognize(context.Context, image.Image) ([]find.OCRWord, error) {
	r.calls++
	return r.words, r.err
}

func TestScreenBundleFindTextUsesVerifiedCaptureAndSessionLifecycle(t *testing.T) {
	region := image.Rect(40, 50, 50, 60)
	frame := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	backend := &textBundleScreenshotter{image: frame, region: region}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	confidence := 88.0
	provider := &bundleTextRecognizer{words: []find.OCRWord{{Text: "Continue", PixelRect: image.Rect(2, 4, 18, 16), Confidence: &confidence, Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: 1}}}
	got, err := session.screen.FindText(context.Background(), region, "Continue", find.TextOptions{Recognizer: provider})
	if err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 || provider.calls != 1 || len(got.Matches) != 1 || got.Matches[0].Rect != image.Rect(41, 52, 49, 58) {
		t.Fatalf("captures=%d provider calls=%d result=%+v", backend.calls, provider.calls, got)
	}
}

func TestScreenBundleFindTextRejectsMissingProviderBeforeCapture(t *testing.T) {
	region := image.Rect(0, 0, 4, 4)
	frame := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	backend := &textBundleScreenshotter{image: frame, region: region}
	session := newSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	_, err := session.screen.FindText(context.Background(), region, "text", find.TextOptions{})
	if !errors.Is(err, find.ErrOCRProviderUnavailable) || backend.calls != 0 {
		t.Fatalf("error=%v captures=%d", err, backend.calls)
	}
}

func TestScreenBundleFindTextRejectsBackendWithoutVerifiedGeometry(t *testing.T) {
	region := image.Rect(0, 0, 4, 4)
	session := newSessionForTesting(&plainTextScreenshotter{image: image.NewNRGBA(region)}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	provider := &bundleTextRecognizer{}
	_, err := session.screen.FindText(context.Background(), region, "text", find.TextOptions{Recognizer: provider})
	if !errors.Is(err, find.ErrOCRGeometry) || provider.calls != 0 {
		t.Fatalf("error=%v recognition calls=%d", err, provider.calls)
	}
}

func TestReviewBackgroundCaptureUnknownVisibilityIsNotForeground(t *testing.T) {
	info := window.Info{NativeID: "42", Title: "Editor", X: 10, Y: 20, W: 4, H: 3}
	backend := &windowPixmapScreenshotter{
		windowCaptureScreenshotter: &windowCaptureScreenshotter{},
		capture: func(context.Context, string) (screen.WindowPixmapCapture, error) {
			return screen.WindowPixmapCapture{Image: image.NewRGBA(image.Rect(0, 0, 4, 3)), ScreenRect: image.Rect(10, 20, 14, 23), Visibility: screen.WindowPixmapUnknown, Foreground: true, ForegroundKnown: true}, nil
		},
	}
	session := newSessionForTesting(backend, nil, newHandleWindowManager(info), nil, nil)
	defer session.Close()
	target, err := session.windows.Find(context.Background(), window.Match{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	got, err := session.screen.CaptureWindowBackground(context.Background(), target)
	if err != nil || got.State != screen.WindowCaptureStateUnknown {
		t.Fatalf("state=%q err=%v; focus does not prove visibility", got.State, err)
	}
}
