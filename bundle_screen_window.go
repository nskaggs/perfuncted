package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/window"
)

// ErrWindowGeometryChanged means the selected window moved, resized, or
// changed visibility while its screen crop was being captured.
var ErrWindowGeometryChanged = errors.New("perfuncted: selected window geometry changed during capture")

// CaptureWindow captures the current screen rectangle reported by a stable
// Session-owned window handle. It captures the visible desktop composite in
// that rectangle, including any windows drawn above the target; it does not
// retrieve hidden or minimized window contents.
func (s *ScreenBundle) CaptureWindow(ctx context.Context, target *Window) (WindowCapture, error) {
	if ctx == nil {
		return WindowCapture{}, fmt.Errorf("perfuncted: capture window: %w: nil context", ErrInvalidArgument)
	}
	if s == nil || s.session == nil {
		return WindowCapture{}, ErrNilSession
	}
	if target == nil || target.id.session == nil || target.id.native == "" {
		return WindowCapture{}, fmt.Errorf("perfuncted: capture window: %w: missing stable window handle", ErrInvalidArgument)
	}
	if target.id.session != s.session {
		return WindowCapture{}, fmt.Errorf("perfuncted: capture window: %w: window belongs to another session", ErrInvalidArgument)
	}
	if err := s.checkAvailable("capture"); err != nil {
		return WindowCapture{}, err
	}
	if err := ctx.Err(); err != nil {
		return WindowCapture{}, err
	}

	selected := target.CachedInfo()
	before, err := target.Info(ctx)
	if err != nil {
		return WindowCapture{}, err
	}
	if !sameWindowCaptureIdentity(selected, before) {
		return WindowCapture{}, fmt.Errorf("perfuncted: capture window %q: %w", target.id.native, ErrWindowIdentityChanged)
	}
	requested, err := windowCaptureRect(before)
	if err != nil {
		return WindowCapture{}, err
	}

	capture, err := s.Capture(ctx, requested)
	if err != nil {
		return WindowCapture{}, err
	}

	after, err := target.Info(ctx)
	if err != nil {
		return WindowCapture{}, err
	}
	if !sameWindowCaptureIdentity(before, after) {
		return WindowCapture{}, fmt.Errorf("perfuncted: capture window %q: %w", target.id.native, ErrWindowIdentityChanged)
	}
	if before.Minimized != after.Minimized || after.Minimized || !sameWindowCaptureGeometry(before, after) {
		return WindowCapture{}, fmt.Errorf("perfuncted: capture window %q: %w", target.id.native, ErrWindowGeometryChanged)
	}
	if err := validateWindowCaptureImage(capture); err != nil {
		return WindowCapture{}, err
	}
	if err := ctx.Err(); err != nil {
		return WindowCapture{}, err
	}
	if err := s.session.ensureOpen(); err != nil {
		return WindowCapture{}, err
	}
	imageBounds := capture.Image.Bounds()
	return WindowCapture{
		Capture:        capture,
		WindowID:       after.StableID(),
		Title:          after.Title,
		AppID:          after.AppID,
		PID:            after.PID,
		ScreenRect:     capture.ScreenRect,
		PixelBounds:    imageBounds,
		PixelScaleX:    float64(imageBounds.Dx()) / float64(capture.ScreenRect.Dx()),
		PixelScaleY:    float64(imageBounds.Dy()) / float64(capture.ScreenRect.Dy()),
		Mode:           WindowCaptureVisibleScreenCrop,
		GeometrySource: "window-manager-reported",
		Occlusion:      "desktop composite; overlapping windows may appear",
	}, nil
}

// CaptureWindowTo captures one stable window's visible screen rectangle and
// atomically publishes it as PNG at path. The captured content is the desktop
// composite, not a background-only buffer.
func (s *ScreenBundle) CaptureWindowTo(ctx context.Context, target *Window, path string) (WindowCapture, error) {
	if path == "" {
		return WindowCapture{}, fmt.Errorf("perfuncted: capture window: %w: empty output path", ErrInvalidArgument)
	}
	capture, err := s.CaptureWindow(ctx, target)
	if err != nil {
		return WindowCapture{}, err
	}
	if err := s.savePNG(ctx, capture.Capture.Image, path); err != nil {
		return WindowCapture{}, err
	}
	return capture, nil
}

func windowCaptureRect(info window.Info) (image.Rectangle, error) {
	if info.Minimized {
		return image.Rectangle{}, fmt.Errorf("perfuncted: minimized window %q has no visible screen crop: %w", info.StableID(), ErrUnsupported)
	}
	if err := window.ValidatePosition(info.X, info.Y); err != nil {
		return image.Rectangle{}, fmt.Errorf("perfuncted: window %q has unsupported screen position: %w", info.StableID(), errors.Join(ErrUnsupported, err))
	}
	if err := window.ValidateSize(info.W, info.H); err != nil {
		return image.Rectangle{}, fmt.Errorf("perfuncted: window %q has unsupported screen bounds: %w", info.StableID(), errors.Join(ErrUnsupported, err))
	}
	maxInt := int(^uint(0) >> 1)
	if info.X > maxInt-info.W || info.Y > maxInt-info.H {
		return image.Rectangle{}, fmt.Errorf("perfuncted: window %q screen bounds overflow: %w", info.StableID(), ErrUnsupported)
	}
	return image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H), nil
}

func sameWindowCaptureIdentity(left, right window.Info) bool {
	return left.StableID() == right.StableID() &&
		left.Title == right.Title &&
		left.AppID == right.AppID &&
		left.Class == right.Class &&
		left.PID == right.PID
}

func sameWindowCaptureGeometry(left, right window.Info) bool {
	return left.X == right.X && left.Y == right.Y && left.W == right.W && left.H == right.H
}

func validateWindowCaptureImage(capture Capture) error {
	bounds := capture.Image.Bounds()
	if bounds.Empty() {
		return fmt.Errorf("perfuncted: screen backend returned an empty window crop: %w", ErrUnsupported)
	}
	for _, point := range []image.Point{
		bounds.Min,
		image.Pt(bounds.Max.X-1, bounds.Max.Y-1),
	} {
		if _, err := capture.ScreenPoint(point); err != nil {
			return fmt.Errorf("perfuncted: screen backend did not cover requested window bounds %v: %w", capture.ScreenRect, errors.Join(ErrUnsupported, err))
		}
	}
	return nil
}
