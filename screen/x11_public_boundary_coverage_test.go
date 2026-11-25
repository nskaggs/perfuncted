//go:build linux

package screen

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11CaptureCancellationPrecedesBackendAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{name: "full frame", run: func() error { _, err := (*X11Backend)(nil).GrabFullHash(ctx); return err }},
		{name: "region", run: func() error {
			_, err := (*X11Backend)(nil).GrabRegionHash(ctx, image.Rect(0, 0, 1, 1))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled capture error = %v, want context.Canceled", err)
			}
		})
	}
}

func TestX11CaptureRejectsInvalidGeometryBeforeImageRequest(t *testing.T) {
	t.Run("empty root framebuffer", func(t *testing.T) {
		backend, conn := newStubScreenX11Backend(t, false)
		backend.screen.WidthInPixels = 0
		imageCalls := 0
		conn.GetImageFunc = func(byte, xproto.Drawable, int16, int16, uint16, uint16, uint32) x11.GetImageCookie {
			imageCalls++
			return x11.NewMockGetImageCookie(nil, nil)
		}
		_, err := backend.GrabFullHash(context.Background())
		if err == nil || !strings.Contains(err.Error(), "empty capture rectangle") || imageCalls != 0 {
			t.Fatalf("empty root framebuffer returned err=%v image calls=%d", err, imageCalls)
		}
	})

	t.Run("unrepresentable region origin", func(t *testing.T) {
		backend, conn := newStubScreenX11Backend(t, false)
		imageCalls := 0
		conn.GetImageFunc = func(byte, xproto.Drawable, int16, int16, uint16, uint16, uint32) x11.GetImageCookie {
			imageCalls++
			return x11.NewMockGetImageCookie(nil, nil)
		}
		_, err := backend.GrabRegionHash(context.Background(), image.Rect(32768, 0, 32769, 1))
		if err == nil || !strings.Contains(err.Error(), "origin out of range") || imageCalls != 0 {
			t.Fatalf("unrepresentable region returned err=%v image calls=%d", err, imageCalls)
		}
	})
}

func TestX11NilBackendLifecycleContracts(t *testing.T) {
	var backend *X11Backend
	if err := backend.Close(); err != nil {
		t.Fatalf("nil backend Close() = %v, want nil", err)
	}
	if _, _, err := backend.Resolution(); err == nil || !strings.Contains(err.Error(), "backend is nil") {
		t.Fatalf("nil backend Resolution() error = %v", err)
	}
}

func TestValidateX11PixelBufferRejectsNonPositiveDimensions(t *testing.T) {
	for _, dims := range [][2]int{{0, 1}, {1, 0}, {-1, 2}} {
		if err := validateX11PixelBuffer(nil, dims[0], dims[1]); err == nil {
			t.Errorf("pixel dimensions %dx%d unexpectedly accepted", dims[0], dims[1])
		}
	}
}
