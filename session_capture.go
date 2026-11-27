package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/v2/internal/capture"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
)

// ErrWindowGeometryChanged means the selected window moved, resized, or
// changed visibility while its screen crop was being captured.
var ErrWindowGeometryChanged = errors.New("perfuncted: selected window geometry changed during capture")

// CaptureWindow captures the current screen rectangle reported by a stable
// Session-owned window handle. It captures the visible desktop composite in
// that rectangle, including any windows drawn above the target; it does not
// retrieve hidden or minimized window contents.
func (s *screenState) CaptureWindow(ctx context.Context, target *windowState) (screen.WindowCapture, error) {
	if err := s.validateWindowCaptureTarget(ctx, target, "capture window"); err != nil {
		return screen.WindowCapture{}, err
	}

	selected := target.CachedInfo()
	before, err := target.Info(ctx)
	if err != nil {
		return screen.WindowCapture{}, err
	}
	if !sameWindowCaptureIdentity(selected, before) {
		return screen.WindowCapture{}, fmt.Errorf("perfuncted: capture window %q: %w", target.id.native, ErrWindowIdentityChanged)
	}
	requested, err := windowCaptureRect(before)
	if err != nil {
		return screen.WindowCapture{}, err
	}

	capture, err := s.Capture(ctx, requested)
	if err != nil {
		return screen.WindowCapture{}, err
	}

	after, err := verifyWindowCaptureTarget(ctx, target, before, "capture window")
	if err != nil {
		return screen.WindowCapture{}, err
	}
	if err := validateWindowCaptureImage(capture); err != nil {
		return screen.WindowCapture{}, err
	}
	if err := ctx.Err(); err != nil {
		return screen.WindowCapture{}, err
	}
	if err := s.session.ensureOpen(); err != nil {
		return screen.WindowCapture{}, err
	}
	imageBounds := capture.Image.Bounds()
	return screen.WindowCapture{
		Capture:        capture,
		WindowID:       after.StableID(),
		Title:          after.Title,
		AppID:          after.AppID,
		PID:            after.PID,
		ScreenRect:     capture.ScreenRect,
		PixelBounds:    imageBounds,
		PixelScaleX:    float64(imageBounds.Dx()) / float64(capture.ScreenRect.Dx()),
		PixelScaleY:    float64(imageBounds.Dy()) / float64(capture.ScreenRect.Dy()),
		Mode:           screen.WindowCaptureVisibleScreenCrop,
		State:          screen.WindowCaptureStateUnknown,
		GeometrySource: "window-manager-reported",
		Occlusion:      "desktop composite; overlapping windows may appear",
	}, nil
}

// CaptureWindowBackground reads a target-only buffer when the selected screen
// backend exposes that capability. It never activates the target and never
// falls back to a visible desktop crop.
func (s *screenState) CaptureWindowBackground(ctx context.Context, target *windowState) (screen.WindowCapture, error) {
	if err := s.validateWindowCaptureTarget(ctx, target, "background capture"); err != nil {
		return screen.WindowCapture{}, err
	}
	selected := target.CachedInfo()
	before, err := target.Info(ctx)
	if err != nil {
		return screen.WindowCapture{}, err
	}
	if !sameWindowCaptureIdentity(selected, before) {
		return screen.WindowCapture{}, fmt.Errorf("perfuncted: background capture %q: %w", target.id.native, ErrWindowIdentityChanged)
	}
	if before.Minimized {
		return screen.WindowCapture{}, errors.Join(ErrUnsupported,
			fmt.Errorf("perfuncted: minimized window %q: %w", target.id.native, screen.ErrWindowCaptureMinimized))
	}
	capturer, ok := s.backend().(interface {
		CaptureWindowPixmap(context.Context, string) (screen.WindowPixmapCapture, error)
	})
	if !ok {
		return screen.WindowCapture{}, s.operationError("background-capture", ErrUnsupported)
	}
	s.traceAction("background-capture window=%s", target.id.native)
	backendCtx, cancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer cancel()
	buffer, captureErr := capturer.CaptureWindowPixmap(backendCtx, target.id.native)
	if ctxErr := backendCtx.Err(); ctxErr != nil {
		return screen.WindowCapture{}, ctxErr
	}
	if openErr := s.session.ensureOpen(); openErr != nil {
		return screen.WindowCapture{}, openErr
	}
	if bufferErr := validateBackgroundBuffer(buffer, captureErr); bufferErr != nil {
		return screen.WindowCapture{}, s.operationError("background-capture", bufferErr)
	}
	after, err := verifyWindowCaptureTarget(ctx, target, before, "background capture")
	if err != nil {
		return screen.WindowCapture{}, err
	}
	if err := ctx.Err(); err != nil {
		return screen.WindowCapture{}, err
	}
	if err := s.session.ensureOpen(); err != nil {
		return screen.WindowCapture{}, err
	}
	state := backgroundCaptureState(buffer)
	imageBounds := buffer.Image.Bounds()
	return screen.WindowCapture{
		Capture:        screen.Capture{Image: buffer.Image, ScreenRect: buffer.ScreenRect},
		WindowID:       after.StableID(),
		Title:          after.Title,
		AppID:          after.AppID,
		PID:            after.PID,
		ScreenRect:     buffer.ScreenRect,
		PixelBounds:    imageBounds,
		PixelScaleX:    1,
		PixelScaleY:    1,
		Mode:           screen.WindowCaptureBackgroundPixmap,
		State:          state,
		GeometrySource: "x11-window-geometry",
		Occlusion:      "target-only Composite pixmap; visibility uses conservative top-level bounding-box overlap",
	}, nil
}

func validateBackgroundBuffer(buffer screen.WindowPixmapCapture, captureErr error) error {
	if captureErr != nil {
		switch {
		case errors.Is(captureErr, screen.ErrBackgroundWindowUnmapped):
			return errors.Join(ErrUnsupported, screen.ErrWindowCaptureUnmapped, captureErr)
		case errors.Is(captureErr, screen.ErrBackgroundWindowCaptureUnsupported):
			return errors.Join(ErrUnsupported, captureErr)
		default:
			return captureErr
		}
	}
	if err := capture.ValidateImageLayout(buffer.Image); err != nil {
		return err
	}
	if buffer.ScreenRect.Empty() || buffer.Image.Bounds().Dx() != buffer.ScreenRect.Dx() || buffer.Image.Bounds().Dy() != buffer.ScreenRect.Dy() {
		return fmt.Errorf("target pixmap geometry does not match its screen rectangle: %w", ErrUnsupported)
	}
	return nil
}

func backgroundCaptureState(buffer screen.WindowPixmapCapture) screen.WindowCaptureState {
	switch {
	case buffer.Visibility == screen.WindowPixmapOccluded:
		return screen.WindowCaptureOccluded
	case buffer.Visibility != screen.WindowPixmapUnobscured || !buffer.ForegroundKnown:
		return screen.WindowCaptureStateUnknown
	case buffer.Foreground:
		return screen.WindowCaptureForeground
	default:
		return screen.WindowCaptureBackgroundVisible
	}
}

func (s *screenState) validateWindowCaptureTarget(ctx context.Context, target *windowState, operation string) error {
	if ctx == nil {
		return fmt.Errorf("perfuncted: %s: %w: nil context", operation, ErrInvalidArgument)
	}
	if s == nil || s.session == nil {
		return ErrNilSession
	}
	if target == nil || target.id.session == nil || target.id.native == "" {
		return fmt.Errorf("perfuncted: %s: %w: missing stable window handle", operation, ErrInvalidArgument)
	}
	if target.id.session != s.session {
		return fmt.Errorf("perfuncted: %s: %w: window belongs to another session", operation, ErrInvalidArgument)
	}
	if err := s.checkAvailable("capture"); err != nil {
		return err
	}
	return ctx.Err()
}

func verifyWindowCaptureTarget(ctx context.Context, target *windowState, before window.Info, operation string) (window.Info, error) {
	after, err := target.Info(ctx)
	if err != nil {
		return window.Info{}, err
	}
	if !sameWindowCaptureIdentity(before, after) {
		return window.Info{}, fmt.Errorf("perfuncted: %s %q: %w", operation, target.id.native, ErrWindowIdentityChanged)
	}
	if before.Minimized != after.Minimized || after.Minimized || !sameWindowCaptureGeometry(before, after) {
		return window.Info{}, fmt.Errorf("perfuncted: %s %q: %w", operation, target.id.native, ErrWindowGeometryChanged)
	}
	return after, nil
}

// CaptureWindowBackgroundTo captures one target-only background buffer and
// atomically publishes it as PNG at path.
func (s *screenState) CaptureWindowBackgroundTo(ctx context.Context, target *windowState, path string) (screen.WindowCapture, error) {
	if path == "" {
		return screen.WindowCapture{}, fmt.Errorf("perfuncted: background capture: %w: empty output path", ErrInvalidArgument)
	}
	capture, err := s.CaptureWindowBackground(ctx, target)
	if err != nil {
		return screen.WindowCapture{}, err
	}
	if err := s.savePNG(ctx, capture.Image, path); err != nil {
		return screen.WindowCapture{}, err
	}
	return capture, nil
}

// CaptureWindowTo captures one stable window's visible screen rectangle and
// atomically publishes it as PNG at path. The captured content is the desktop
// composite, not a background-only buffer.
func (s *screenState) CaptureWindowTo(ctx context.Context, target *windowState, path string) (screen.WindowCapture, error) {
	if path == "" {
		return screen.WindowCapture{}, fmt.Errorf("perfuncted: capture window: %w: empty output path", ErrInvalidArgument)
	}
	capture, err := s.CaptureWindow(ctx, target)
	if err != nil {
		return screen.WindowCapture{}, err
	}
	if err := s.savePNG(ctx, capture.Image, path); err != nil {
		return screen.WindowCapture{}, err
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

func validateWindowCaptureImage(capture screen.Capture) error {
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

// Capture captures this window's visible desktop rectangle. Overlapping
// windows may appear in the image; it does not activate or unocclude the target.
func (w *Window) Capture() (screen.WindowCapture, error) {
	if w == nil || w.state == nil || w.state.id.session == nil {
		return screen.WindowCapture{}, ErrNilSession
	}
	s := &Session{core: w.state.id.session, scope: w.scope}
	return s.Screen().CaptureWindow(w)
}
