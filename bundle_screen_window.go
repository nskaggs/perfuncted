package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/screen"
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
		State:          WindowCaptureStateUnknown,
		GeometrySource: "window-manager-reported",
		Occlusion:      "desktop composite; overlapping windows may appear",
	}, nil
}

// CaptureWindowBackground reads a target-only buffer when the selected screen
// backend exposes that capability. It never activates the target and never
// falls back to a visible desktop crop.
func (s *ScreenBundle) CaptureWindowBackground(ctx context.Context, target *Window) (WindowCapture, error) {
	if ctx == nil {
		return WindowCapture{}, fmt.Errorf("perfuncted: background capture: %w: nil context", ErrInvalidArgument)
	}
	if s == nil || s.session == nil {
		return WindowCapture{}, ErrNilSession
	}
	if target == nil || target.id.session == nil || target.id.native == "" {
		return WindowCapture{}, fmt.Errorf("perfuncted: background capture: %w: missing stable window handle", ErrInvalidArgument)
	}
	if target.id.session != s.session {
		return WindowCapture{}, fmt.Errorf("perfuncted: background capture: %w: window belongs to another session", ErrInvalidArgument)
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
		return WindowCapture{}, fmt.Errorf("perfuncted: background capture %q: %w", target.id.native, ErrWindowIdentityChanged)
	}
	if before.Minimized {
		return WindowCapture{}, errors.Join(ErrUnsupported,
			fmt.Errorf("perfuncted: minimized window %q: %w", target.id.native, ErrWindowCaptureMinimized))
	}
	capturer, ok := s.backend.(interface {
		CaptureWindowPixmap(context.Context, string) (screen.WindowPixmapCapture, error)
	})
	if !ok {
		return WindowCapture{}, s.operationError("background-capture", ErrUnsupported)
	}
	s.traceAction("background-capture window=%s", target.id.native)
	backendCtx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	buffer, captureErr := capturer.CaptureWindowPixmap(backendCtx, target.id.native)
	if ctxErr := backendCtx.Err(); ctxErr != nil {
		return WindowCapture{}, ctxErr
	}
	if openErr := s.session.ensureOpen(); openErr != nil {
		return WindowCapture{}, openErr
	}
	if captureErr != nil {
		if errors.Is(captureErr, screen.ErrBackgroundWindowUnmapped) {
			captureErr = errors.Join(ErrUnsupported, ErrWindowCaptureUnmapped, captureErr)
		} else if errors.Is(captureErr, screen.ErrBackgroundWindowCaptureUnsupported) {
			captureErr = errors.Join(ErrUnsupported, captureErr)
		}
		return WindowCapture{}, s.operationError("background-capture", captureErr)
	}
	if err := validateCaptureImageLayout(buffer.Image); err != nil {
		return WindowCapture{}, s.operationError("background-capture", err)
	}
	if buffer.ScreenRect.Empty() || buffer.Image.Bounds().Dx() != buffer.ScreenRect.Dx() || buffer.Image.Bounds().Dy() != buffer.ScreenRect.Dy() {
		return WindowCapture{}, s.operationError("background-capture", fmt.Errorf("target pixmap geometry does not match its screen rectangle: %w", ErrUnsupported))
	}
	after, err := target.Info(ctx)
	if err != nil {
		return WindowCapture{}, err
	}
	if !sameWindowCaptureIdentity(before, after) {
		return WindowCapture{}, fmt.Errorf("perfuncted: background capture %q: %w", target.id.native, ErrWindowIdentityChanged)
	}
	if before.Minimized != after.Minimized || after.Minimized || !sameWindowCaptureGeometry(before, after) {
		return WindowCapture{}, fmt.Errorf("perfuncted: background capture %q: %w", target.id.native, ErrWindowGeometryChanged)
	}
	if err := ctx.Err(); err != nil {
		return WindowCapture{}, err
	}
	if err := s.session.ensureOpen(); err != nil {
		return WindowCapture{}, err
	}
	state := WindowCaptureStateUnknown
	switch buffer.Visibility {
	case screen.WindowPixmapUnobscured:
		if !buffer.ForegroundKnown {
			state = WindowCaptureStateUnknown
		} else if buffer.Foreground {
			state = WindowCaptureForeground
		} else {
			state = WindowCaptureBackgroundVisible
		}
	case screen.WindowPixmapOccluded:
		state = WindowCaptureOccluded
	default:
		if buffer.ForegroundKnown && buffer.Foreground {
			state = WindowCaptureForeground
		}
	}
	imageBounds := buffer.Image.Bounds()
	return WindowCapture{
		Capture:        Capture{Image: buffer.Image, ScreenRect: buffer.ScreenRect},
		WindowID:       after.StableID(),
		Title:          after.Title,
		AppID:          after.AppID,
		PID:            after.PID,
		ScreenRect:     buffer.ScreenRect,
		PixelBounds:    imageBounds,
		PixelScaleX:    1,
		PixelScaleY:    1,
		Mode:           WindowCaptureBackgroundPixmap,
		State:          state,
		GeometrySource: "x11-window-geometry",
		Occlusion:      "target-only Composite pixmap; visibility uses conservative top-level bounding-box overlap",
	}, nil
}

// CaptureWindowBackgroundTo captures one target-only background buffer and
// atomically publishes it as PNG at path.
func (s *ScreenBundle) CaptureWindowBackgroundTo(ctx context.Context, target *Window, path string) (WindowCapture, error) {
	if path == "" {
		return WindowCapture{}, fmt.Errorf("perfuncted: background capture: %w: empty output path", ErrInvalidArgument)
	}
	capture, err := s.CaptureWindowBackground(ctx, target)
	if err != nil {
		return WindowCapture{}, err
	}
	if err := s.savePNG(ctx, capture.Capture.Image, path); err != nil {
		return WindowCapture{}, err
	}
	return capture, nil
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
