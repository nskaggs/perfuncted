package perfuncted

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/util"
	"github.com/nskaggs/perfuncted/screen"
)

// ScreenBundle wraps a screen.Screenshotter with additional find utilities.
type ScreenBundle struct {
	screen.Screenshotter
}

// Close delegates to the underlying Screenshotter Close method.
func (s ScreenBundle) Close() error {
	if s.Screenshotter == nil {
		return nil
	}
	return s.Screenshotter.Close()
}

func (s ScreenBundle) checkAvailable() error {
	return util.CheckAvailable("screen", s.Screenshotter)
}

func (s ScreenBundle) GrabHash(rect image.Rectangle) (uint32, error) {
	return s.GrabHashContext(context.Background(), rect)
}

func (s ScreenBundle) GrabHashContext(ctx context.Context, rect image.Rectangle) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	if rect.Empty() {
		return s.Screenshotter.GrabFullHash(ctx)
	}
	return find.GrabHash(ctx, s.Screenshotter, rect, nil)
}

func (s ScreenBundle) GrabFullHash() (uint32, error) {
	return s.GrabFullHashContext(context.Background())
}

func (s ScreenBundle) GrabFullHashContext(ctx context.Context) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return s.Screenshotter.GrabFullHash(ctx)
}

func (s ScreenBundle) Grab(rect image.Rectangle) (image.Image, error) {
	return s.GrabContext(context.Background(), rect)
}

func (s ScreenBundle) GrabContext(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	if rect.Empty() {
		// Normalize: empty rect means full-screen grab at bundle level.
		return s.Screenshotter.Grab(ctx, image.Rectangle{})
	}
	return s.Screenshotter.Grab(ctx, rect)
}

func (s ScreenBundle) GrabFull() (image.Image, error) {
	return s.GrabFullContext(context.Background())
}

func (s ScreenBundle) GrabFullContext(ctx context.Context) (image.Image, error) {
	return s.GrabContext(ctx, image.Rectangle{})
}

func (s ScreenBundle) CaptureRegion(rect image.Rectangle, path string) error {
	return s.CaptureRegionContext(context.Background(), rect, path)
}

func (s ScreenBundle) CaptureRegionContext(ctx context.Context, rect image.Rectangle, path string) error {
	img, err := s.GrabContext(ctx, rect)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func (s ScreenBundle) GetPixel(x, y int) (color.RGBA, error) {
	return s.GetPixelContext(context.Background(), x, y)
}

func (s ScreenBundle) GetPixelContext(ctx context.Context, x, y int) (color.RGBA, error) {
	if err := s.checkAvailable(); err != nil {
		return color.RGBA{}, err
	}
	c, err := find.FirstPixel(ctx, s.Screenshotter, image.Rect(x, y, x+1, y+1))
	if err != nil {
		return color.RGBA{}, err
	}
	return c, nil
}

func (s ScreenBundle) GetMultiplePixels(points []image.Point) ([]color.RGBA, error) {
	return s.GetMultiplePixelsContext(context.Background(), points)
}

func (s ScreenBundle) GetMultiplePixelsContext(ctx context.Context, points []image.Point) ([]color.RGBA, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	out := make([]color.RGBA, len(points))
	if len(points) == 0 {
		return out, nil
	}
	// Compute bounding box for a single grab, then index into the returned image.
	minX, minY := points[0].X, points[0].Y
	maxX, maxY := minX, minY
	for _, p := range points {
		if p.X < minX {
			minX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	bounds := image.Rect(minX, minY, maxX+1, maxY+1)
	img, err := s.GrabContext(ctx, bounds)
	if err != nil {
		return nil, err
	}
	for i, p := range points {
		c := color.RGBAModel.Convert(img.At(p.X, p.Y)).(color.RGBA)
		out[i] = c
	}
	return out, nil
}

// PixelToScreen currently returns the rectangle's Min point. If a real
// coordinate conversion is required by some backends, implement it here.
// TODO: remove this helper if unused or implement proper conversion.
func (s ScreenBundle) PixelToScreen(rect image.Rectangle) (int, int, error) {
	return rect.Min.X, rect.Min.Y, nil
}

func (s ScreenBundle) WaitForFn(rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	return s.WaitForFnContext(context.Background(), rect, fn, poll)
}

func (s ScreenBundle) WaitForAnyChange(timeout time.Duration) {
	_ = s.WaitForAnyChangeContext(context.Background(), timeout)
}

func (s ScreenBundle) WaitForAnyChangeContext(ctx context.Context, timeout time.Duration) error {
	if err := s.checkAvailable(); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// WaitForVisibleChangeContext returns when any visible change occurs. Ignore
	// errors and timeouts — this helper intentionally never returns an error.
	_, _ = s.WaitForVisibleChangeContext(cctx, image.Rectangle{}, 0, 1)
	return nil
}

func (s ScreenBundle) WaitForStableOrTimeout(rect image.Rectangle, timeout time.Duration) {
	_ = s.WaitForStableOrTimeoutContext(context.Background(), rect, timeout)
}

func (s ScreenBundle) WaitForStableOrTimeoutContext(ctx context.Context, rect image.Rectangle, timeout time.Duration) error {
	if err := s.checkAvailable(); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Default to waiting for 3 stable samples; the function never returns an error
	// on timeout — it simply returns after timeout elapses.
	_, _ = find.WaitForNoChange(cctx, s.Screenshotter, rect, 3, 0, nil)
	return nil
}

func (s ScreenBundle) WaitForFnContext(ctx context.Context, rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	return find.WaitForFn(ctx, s.Screenshotter, rect, fn, poll)
}

func (s ScreenBundle) WaitForVisibleChange(rect image.Rectangle, poll time.Duration, stable int) (uint32, error) {
	return s.WaitForVisibleChangeContext(context.Background(), rect, poll, stable)
}

func (s ScreenBundle) WaitForVisibleChangeContext(ctx context.Context, rect image.Rectangle, poll time.Duration, stable int) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	initial, err := s.GrabHashContext(ctx, rect)
	if err != nil {
		return 0, err
	}
	h, err := find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
	if err != nil {
		return 0, err
	}
	if stable > 1 {
		return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
	}
	return h, nil
}

func (s ScreenBundle) WaitForStable(rect image.Rectangle, stableN int, poll time.Duration) (uint32, error) {
	return s.WaitForStableContext(context.Background(), rect, stableN, poll)
}

func (s ScreenBundle) WaitForStableContext(ctx context.Context, rect image.Rectangle, stableN int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stableN, poll, nil)
}

func (s ScreenBundle) WaitForSettle(rect image.Rectangle, action func(), stable int, poll time.Duration) (uint32, error) {
	return s.WaitForSettleContext(context.Background(), rect, action, stable, poll)
}

func (s ScreenBundle) WaitForSettleContext(ctx context.Context, rect image.Rectangle, action func(), stable int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	before, err := s.GrabHashContext(ctx, rect)
	if err != nil {
		return 0, err
	}
	action()
	if _, err := find.WaitForChange(ctx, s.Screenshotter, rect, before, poll, nil); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

func (s ScreenBundle) LocateExact(rect image.Rectangle, reference image.Image) (image.Rectangle, error) {
	return s.LocateExactContext(context.Background(), rect, reference)
}

func (s ScreenBundle) LocateExactContext(ctx context.Context, searchArea image.Rectangle, reference image.Image) (image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	return find.LocateExact(ctx, s.Screenshotter, searchArea, reference)
}

func (s ScreenBundle) WaitForLocate(rect image.Rectangle, reference image.Image, poll time.Duration) (image.Rectangle, error) {
	return s.WaitForLocateContext(context.Background(), rect, reference, poll)
}

func (s ScreenBundle) WaitForLocateContext(ctx context.Context, searchArea image.Rectangle, reference image.Image, poll time.Duration) (image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	return find.WaitForLocate(ctx, s.Screenshotter, searchArea, reference, poll)
}

func (s ScreenBundle) WaitWithTolerance(rect image.Rectangle, reference image.Image, radius int, poll time.Duration) (uint32, image.Rectangle, error) {
	return s.WaitWithToleranceContext(context.Background(), rect, reference, radius, poll)
}

func (s ScreenBundle) WaitWithToleranceContext(ctx context.Context, rect image.Rectangle, reference image.Image, radius int, poll time.Duration) (uint32, image.Rectangle, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, image.Rectangle{}, err
	}
	return find.WaitWithTolerance(ctx, s.Screenshotter, rect, reference, radius, poll, nil)
}

func (s ScreenBundle) FindColor(rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	return s.FindColorContext(context.Background(), rect, target, tolerance)
}

func (s ScreenBundle) FindColorContext(ctx context.Context, rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	if err := s.checkAvailable(); err != nil {
		return image.Point{}, err
	}
	return find.FindColor(ctx, s.Screenshotter, rect, target, tolerance)
}

func (s ScreenBundle) WaitForNoChange(rect image.Rectangle, stable int, poll time.Duration) (uint32, error) {
	return s.WaitForNoChangeContext(context.Background(), rect, stable, poll)
}

func (s ScreenBundle) WaitForNoChangeContext(ctx context.Context, rect image.Rectangle, stable int, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

func (s ScreenBundle) WaitForChange(rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	return s.WaitForChangeContext(context.Background(), rect, initial, poll)
}

func (s ScreenBundle) WaitForChangeContext(ctx context.Context, rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
}

func (s ScreenBundle) WaitFor(rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	return s.WaitForContext(context.Background(), rect, want, poll)
}

func (s ScreenBundle) WaitForContext(ctx context.Context, rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitFor(ctx, s.Screenshotter, rect, want, poll, nil)
}

func (s ScreenBundle) ScanFor(rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	return s.ScanForContext(context.Background(), rects, wants, poll)
}

func (s ScreenBundle) ScanForContext(ctx context.Context, rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	if err := s.checkAvailable(); err != nil {
		return find.Result{}, err
	}
	return find.ScanFor(ctx, s.Screenshotter, rects, wants, poll, nil)
}

func (s ScreenBundle) Resolution() (int, int, error) {
	return s.ResolutionContext(context.Background())
}

func (s ScreenBundle) ResolutionContext(ctx context.Context) (int, int, error) {
	if err := s.checkAvailable(); err != nil {
		return 0, 0, err
	}
	return screen.ResolutionWithContext(ctx, s.Screenshotter)
}
