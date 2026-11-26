package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
	"image"
	"image/color"
	"image/png"
	"math/bits"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestCaptureScreenPoint(t *testing.T) {
	tests := []struct {
		name    string
		capture Capture
		pixel   image.Point
		want    image.Point
		wantErr bool
	}{
		{
			name:    "one to one",
			capture: Capture{Image: image.NewRGBA(image.Rect(0, 0, 4, 3)), ScreenRect: image.Rect(10, 20, 14, 23)},
			pixel:   image.Pt(3, 2),
			want:    image.Pt(13, 22),
		},
		{
			name:    "rebased image",
			capture: Capture{Image: image.NewRGBA(image.Rect(100, 200, 104, 204)), ScreenRect: image.Rect(10, 20, 14, 24)},
			pixel:   image.Pt(101, 202),
			want:    image.Pt(11, 22),
		},
		{
			name:    "smaller capture than the region",
			capture: Capture{Image: image.NewRGBA(image.Rect(5, 7, 7, 9)), ScreenRect: image.Rect(-20, 30, -12, 38)},
			pixel:   image.Pt(6, 8),
			wantErr: true,
		},
		{
			name:    "rebased smaller capture than the region",
			capture: Capture{Image: image.NewRGBA(image.Rect(5, 7, 7, 9)), ScreenRect: image.Rect(0, 0, 8, 8)},
			pixel:   image.Pt(6, 8),
			wantErr: true,
		},
		{
			name:    "nil image",
			capture: Capture{ScreenRect: image.Rect(0, 0, 1, 1)},
			pixel:   image.Pt(0, 0),
			wantErr: true,
		},
		{
			name:    "empty image",
			capture: Capture{Image: image.NewRGBA(image.Rect(0, 0, 0, 1)), ScreenRect: image.Rect(0, 0, 1, 1)},
			pixel:   image.Pt(0, 0),
			wantErr: true,
		},
		{
			name:    "empty screen region",
			capture: Capture{Image: image.NewRGBA(image.Rect(0, 0, 1, 1))},
			pixel:   image.Pt(0, 0),
			wantErr: true,
		},
		{
			name:    "exclusive max x",
			capture: Capture{Image: image.NewRGBA(image.Rect(5, 7, 7, 9)), ScreenRect: image.Rect(0, 0, 2, 2)},
			pixel:   image.Pt(7, 8),
			wantErr: true,
		},
		{
			name:    "exclusive max y",
			capture: Capture{Image: image.NewRGBA(image.Rect(5, 7, 7, 9)), ScreenRect: image.Rect(0, 0, 2, 2)},
			pixel:   image.Pt(6, 9),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.capture.ScreenPoint(tt.pixel)
			if tt.wantErr {
				if err == nil || !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("ScreenPoint error = %v, want ErrInvalidArgument", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ScreenPoint: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ScreenPoint(%v) = %v, want %v", tt.pixel, got, tt.want)
			}
		})
	}
}

func TestCaptureScreenPointAvoidsIntermediateOverflow(t *testing.T) {
	span := uint64(1) << (bits.UintSize - 2)
	offset := span / 2
	capture := Capture{
		Image:      captureBoundsImage{bounds: image.Rect(0, 0, int(span), int(span))},
		ScreenRect: image.Rect(0, 0, int(span), int(span)),
	}

	got, err := capture.ScreenPoint(image.Pt(int(offset), int(offset)))
	if err != nil {
		t.Fatalf("ScreenPoint: %v", err)
	}
	if want := image.Pt(int(offset), int(offset)); got != want {
		t.Fatalf("ScreenPoint = %v, want %v", got, want)
	}
}

func TestCaptureScreenPointRejectsTypedNilImage(t *testing.T) {
	var typedNil *image.RGBA
	capture := Capture{Image: typedNil, ScreenRect: image.Rect(0, 0, 1, 1)}

	_, err := capture.ScreenPoint(image.Pt(0, 0))
	if err == nil || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ScreenPoint error = %v, want ErrInvalidArgument", err)
	}
}

type captureBoundsImage struct {
	bounds image.Rectangle
}

func (i captureBoundsImage) ColorModel() color.Model { return color.RGBAModel }

func (i captureBoundsImage) Bounds() image.Rectangle { return i.bounds }

func (captureBoundsImage) At(int, int) color.Color { return color.RGBA{} }

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

// A capture whose axes describe different scales is not a scaling of the region at all,
// and translatePointsToImage refuses it. ScreenPoint refusing it too is what keeps the
// two directions from disagreeing about the same capture: it used to accept this one and
// hand back a coordinate whose inverse the package disowns.
func TestCaptureScreenPointRefusesANonUniformCapture(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		region image.Rectangle
		bounds image.Rectangle
	}{
		{"stretched on one axis", image.Rect(0, 0, 100, 100), image.Rect(0, 0, 200, 100)},
		{"stretched on the other", image.Rect(0, 0, 100, 100), image.Rect(0, 0, 100, 200)},
		{"rebased and stretched", image.Rect(0, 0, 100, 100), image.Rect(5, 7, 205, 107)},
		{"square capture on a wide region", image.Rect(0, 0, 200, 100), image.Rect(0, 0, 100, 100)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			region := tc.region
			capture := Capture{Image: image.NewRGBA(tc.bounds), ScreenRect: region}
			if _, err := capture.ScreenPoint(image.Point{tc.bounds.Min.X + 1, tc.bounds.Min.Y + 1}); err == nil {
				t.Fatalf("ScreenPoint accepted a capture that is not a uniform scaling of the region: %v", tc.bounds)
			}
			if _, err := translatePointsToImage([]image.Point{{X: 1, Y: 1}}, region, tc.bounds); err == nil {
				t.Fatalf("translatePointsToImage accepted a capture that is not a uniform scaling of the region: %v", tc.bounds)
			}
		})
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
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := session.screen.CaptureWindowBackground(context.Background(), target)
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
			session := newSessionForTesting(backend, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
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
			target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.screen.CaptureWindowBackground(context.Background(), target)
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
	session := newSessionForTesting(backend, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.screen.CaptureWindowBackground(context.Background(), target); !errors.Is(err, ErrUnsupported) || !errors.Is(err, ErrWindowCaptureUnmapped) {
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
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
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
			session := newSessionForTesting(backend, nil, manager, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
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
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
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
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Scaled"})
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
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Offscreen"})
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
			target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: tc.info.Title})
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
			target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
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
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
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
	target, err := first.windows.Find(context.Background(), WindowMatch{TitleExact: "Editor"})
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
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
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
			target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: info.Title})
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
