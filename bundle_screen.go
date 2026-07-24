package perfuncted

import (
	"context"
	"image"

	"github.com/nskaggs/perfuncted/screen"
)

type ScreenBundle struct {
	screen.Screenshotter
	bundleBase
}

func (s *ScreenBundle) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if s.Screenshotter == nil {
		return nil, &CapabilityError{Cap: CapabilityScreen, Err: ErrNotAvailable}
	}
	s.traceAction("screen", "grab")
	return s.Screenshotter.Grab(ctx, rect)
}

func (s *ScreenBundle) GrabFullHash(ctx context.Context) (uint32, error) {
	if s.Screenshotter == nil {
		return 0, &CapabilityError{Cap: CapabilityScreen, Err: ErrNotAvailable}
	}
	s.traceAction("screen", "grab-full-hash")
	return s.Screenshotter.GrabFullHash(ctx)
}

func (s *ScreenBundle) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	if s.Screenshotter == nil {
		return 0, &CapabilityError{Cap: CapabilityScreen, Err: ErrNotAvailable}
	}
	s.traceAction("screen", "grab-region-hash")
	return s.Screenshotter.GrabRegionHash(ctx, rect)
}
