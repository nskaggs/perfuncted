package screen

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"

	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
)

// GrabVerified captures the requested GNOME logical desktop region only when
// the native bridge's authoritative screenshot metadata proves that the PNG
// represents every requested pixel. An ambiguous, clipped, or fractionally
// scaled capture is rejected instead of publishing inaccurate desktop points.
//
// This intentionally uses CaptureRegion rather than Grab: Grab normalizes
// images after discarding the compositor's capture geometry metadata.
//
//nolint:gocyclo // Compositor capture geometry and cleanup are validated together.
func (b *GnomeNativeScreenBackend) GrabVerified(ctx context.Context, rect image.Rectangle) (result find.VerifiedTemplateCapture, retErr error) {
	ctx = contextutil.Default(ctx)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if b == nil || b.bridge == nil {
		return result, fmt.Errorf("screen/gnome-native: backend is not initialised")
	}
	if rect.Empty() {
		return result, fmt.Errorf("screen/gnome-native: verified capture requires nonempty rectangle")
	}
	if err := validateGnomeCaptureRect(rect); err != nil {
		return result, err
	}
	file, err := os.CreateTemp("", "perfuncted-gnome-verified-*.png")
	if err != nil {
		return result, fmt.Errorf("screen/gnome-native: create verified screenshot transport: %w", err)
	}
	path := file.Name()
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("screen/gnome-native: close verified transport: %w", closeErr))
			result = find.VerifiedTemplateCapture{}
		}
		if removeErr := os.Remove(path); removeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("screen/gnome-native: remove verified transport: %w", removeErr))
			result = find.VerifiedTemplateCapture{}
		}
	}()
	captured, err := b.bridge.CaptureRegion(ctx, int(file.Fd()), rect)
	if err != nil {
		return result, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}
	if captured.X != int32(rect.Min.X) || captured.Y != int32(rect.Min.Y) ||
		int(captured.Width) != rect.Dx() || int(captured.Height) != rect.Dy() {
		return result, fmt.Errorf("screen/gnome-native: captured desktop region does not match requested region: %w", find.ErrUnverifiedCaptureGeometry)
	}
	// The compositor's stage capture scale is independent of the requested
	// PNG dimensions. A partial capture could otherwise look like a lower
	// apparent image scale after normalization.
	if math.IsNaN(captured.Scale) || math.IsInf(captured.Scale, 0) ||
		captured.Scale < 1 || captured.Scale > math.MaxInt32 || math.Trunc(captured.Scale) != captured.Scale {
		return result, fmt.Errorf("screen/gnome-native: unsupported or unverified screenshot scale %g: %w", captured.Scale, find.ErrUnverifiedCaptureGeometry)
	}
	scale := int(captured.Scale)
	maxInt := int(^uint(0) >> 1)
	if rect.Dx() > maxInt/scale || rect.Dy() > maxInt/scale ||
		int(captured.PixelWidth) != rect.Dx()*scale || int(captured.PixelHeight) != rect.Dy()*scale {
		return result, fmt.Errorf("screen/gnome-native: actual pixel geometry does not cover requested region: %w", find.ErrUnverifiedCaptureGeometry)
	}
	if _, seekErr := file.Seek(0, 0); seekErr != nil {
		return result, fmt.Errorf("screen/gnome-native: rewind verified screenshot transport: %w", seekErr)
	}
	pixels, err := png.Decode(file)
	if err != nil {
		return result, fmt.Errorf("screen/gnome-native: decode verified PNG: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if pixels.Bounds().Dx() != int(captured.PixelWidth) || pixels.Bounds().Dy() != int(captured.PixelHeight) {
		return result, fmt.Errorf("screen/gnome-native: PNG does not match reported pixel coverage: %w", find.ErrUnverifiedCaptureGeometry)
	}
	return find.VerifiedTemplateCapture{Image: pixels, DesktopRect: rect, PixelScale: scale}, nil
}
