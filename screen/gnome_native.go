//go:build linux
// +build linux

package screen

import (
	"context"
	"fmt"
	"image"
	"image/png"
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
	if rect.Empty() {
		err = b.bridge.CaptureFull(ctx, int(f.Fd()))
	} else {
		err = b.bridge.CaptureRegion(ctx, int(f.Fd()), rect)
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
	return img, nil
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
