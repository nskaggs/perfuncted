//go:build linux
// +build linux

package screen

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/contextutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

var _ Screenshotter = (*GnomeNativeScreenBackend)(nil)

// GnomeNativeScreenBackend captures through Shell.Screenshot in the bundled
// extension. The only filesystem object is a private caller-owned temporary
// file used as the seekable Unix-FD transport.
type GnomeNativeScreenBackend struct {
	bridge *gnomebridge.Client
}

// NewGnomeNativeScreenBackendForRuntime connects to the versioned bridge.
func NewGnomeNativeScreenBackendForRuntime(rt env.Runtime) (*GnomeNativeScreenBackend, error) {
	bridge, err := gnomebridge.ConnectRuntime(context.Background(), rt)
	if err != nil {
		return nil, err
	}
	if !bridge.HasCapability(gnomebridge.CapabilityScreen) {
		_ = bridge.Close()
		return nil, fmt.Errorf("gnome screen: bridge does not advertise screen capability")
	}
	return &GnomeNativeScreenBackend{bridge: bridge}, nil
}

// Grab captures the full desktop or a non-empty global logical-screen region.
func (b *GnomeNativeScreenBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.bridge == nil {
		return nil, fmt.Errorf("screen/gnome-native: backend is not initialised")
	}
	f, err := os.CreateTemp("", "perfuncted-gnome-capture-*.png")
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-native: create transport: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	defer f.Close()
	var capture gnomebridge.ScreenCapture
	if rect.Empty() {
		capture, err = b.bridge.CaptureFull(ctx, int(f.Fd()))
	} else {
		capture, err = b.bridge.CaptureRegion(ctx, int(f.Fd()), rect)
	}
	if err != nil {
		return nil, err
	}
	if _, seekErr := f.Seek(0, 0); seekErr != nil {
		return nil, fmt.Errorf("screen/gnome-native: rewind transport: %w", seekErr)
	}
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("screen/gnome-native: decode PNG: %w", err)
	}
	return normalizeGnomeCapture(img, capture)
}

func normalizeGnomeCapture(img image.Image, capture gnomebridge.ScreenCapture) (image.Image, error) {
	if img == nil {
		return nil, fmt.Errorf("screen/gnome-native: capture returned a nil image")
	}
	if capture.Width <= 0 || capture.Height <= 0 ||
		capture.PixelWidth <= 0 || capture.PixelHeight <= 0 ||
		capture.Scale <= 0 || math.IsNaN(capture.Scale) || math.IsInf(capture.Scale, 0) {
		return nil, fmt.Errorf("screen/gnome-native: invalid capture geometry %#v", capture)
	}
	if got := img.Bounds(); got.Dx() != int(capture.PixelWidth) || got.Dy() != int(capture.PixelHeight) {
		return nil, fmt.Errorf(
			"screen/gnome-native: PNG dimensions %dx%d do not match capture geometry %dx%d",
			got.Dx(),
			got.Dy(),
			capture.PixelWidth,
			capture.PixelHeight,
		)
	}
	if capture.Width == capture.PixelWidth && capture.Height == capture.PixelHeight {
		return img, nil
	}
	return resizeNearest(img, int(capture.Width), int(capture.Height)), nil
}

func resizeNearest(src image.Image, width, height int) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	srcBounds := src.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()
	for y := 0; y < height; y++ {
		srcY := srcBounds.Min.Y + y*srcHeight/height
		for x := 0; x < width; x++ {
			srcX := srcBounds.Min.X + x*srcWidth/width
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

// GrabFullHash returns a pixel hash for the full screen.
func (b *GnomeNativeScreenBackend) GrabFullHash(ctx context.Context) (uint32, error) {
	img, err := b.Grab(ctx, image.Rectangle{})
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

// GrabRegionHash returns a pixel hash for a screen region.
func (b *GnomeNativeScreenBackend) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := b.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return find.PixelHash(img, nil), nil
}

// Resolution returns the dimensions of a full-screen capture.
func (b *GnomeNativeScreenBackend) Resolution() (int, int, error) {
	return b.ResolutionWithContext(context.Background())
}

// ResolutionWithContext preserves the caller's cancellation and deadline
// while using the same full-screen capture path as Resolution.
func (b *GnomeNativeScreenBackend) ResolutionWithContext(ctx context.Context) (int, int, error) {
	img, err := b.Grab(ctx, image.Rectangle{})
	if err != nil {
		return 0, 0, err
	}
	return img.Bounds().Dx(), img.Bounds().Dy(), nil
}

// Close releases the GNOME bridge connection.
func (b *GnomeNativeScreenBackend) Close() error {
	if b == nil || b.bridge == nil {
		return nil
	}
	return b.bridge.Close()
}
