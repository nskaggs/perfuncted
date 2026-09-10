package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math/bits"
	"testing"

	"github.com/nskaggs/perfuncted/screen"
)

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
			name:    "scaled image",
			capture: Capture{Image: image.NewRGBA(image.Rect(5, 7, 7, 9)), ScreenRect: image.Rect(-20, 30, -12, 38)},
			pixel:   image.Pt(6, 8),
			want:    image.Pt(-16, 34),
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
			name:    "out of bounds",
			capture: Capture{Image: image.NewRGBA(image.Rect(5, 7, 7, 9)), ScreenRect: image.Rect(0, 0, 2, 2)},
			pixel:   image.Pt(7, 8),
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
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	wantRect := image.Rect(100, 200, 104, 204)
	capture, err := session.Screen.Capture(context.Background(), wantRect)
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
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	capture, err := session.Screen.Capture(context.Background(), image.Rectangle{})
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
