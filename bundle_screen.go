package perfuncted

import (
	"context"
	"fmt"
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
	tracer *actionTracer
}

// close delegates to the underlying Screenshotter Close method.
func (s ScreenBundle) close() error {
	if s.Screenshotter == nil {
		return nil
	}
	s.traceAction("close")
	return s.Screenshotter.Close()
}

func (s ScreenBundle) checkAvailable() error {
	return util.CheckAvailable("screen", s.Screenshotter)
}

func (s ScreenBundle) traceAction(msg string) {
	if s.tracer == nil {
		return
	}
	s.tracer.Tracef("screen", "%s", msg)
}

func (s ScreenBundle) GrabHash(rect image.Rectangle) (uint32, error) {
	return s.grabHashContext(context.Background(), rect)
}

func (s ScreenBundle) grabHashContext(ctx context.Context, rect image.Rectangle) (uint32, error) {
	s.traceAction(fmt.Sprintf("grab-hash rect=%s", rect))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	if rect.Empty() {
		return s.Screenshotter.GrabFullHash(ctx)
	}
	return find.GrabHash(ctx, s.Screenshotter, rect, nil)
}

func (s ScreenBundle) grabFullHash() (uint32, error) {
	return s.grabFullHashContext(context.Background())
}

func (s ScreenBundle) grabFullHashContext(ctx context.Context) (uint32, error) {
	s.traceAction("grab-full-hash")
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return s.Screenshotter.GrabFullHash(ctx)
}

func (s ScreenBundle) Grab(rect image.Rectangle) (image.Image, error) {
	return s.grabContext(context.Background(), rect)
}

func (s ScreenBundle) grabContext(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.traceAction(fmt.Sprintf("grab rect=%s", rect))
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	if rect.Empty() {
		return s.Screenshotter.Grab(ctx, image.Rectangle{})
	}
	return s.Screenshotter.Grab(ctx, rect)
}

func (s ScreenBundle) grabFull() (image.Image, error) {
	return s.grabContext(context.Background(), image.Rectangle{})
}

func (s ScreenBundle) grabFullContext(ctx context.Context) (image.Image, error) {
	return s.grabContext(ctx, image.Rectangle{})
}

func (s ScreenBundle) CaptureRegion(rect image.Rectangle, path string) error {
	return s.captureRegionContext(context.Background(), rect, path)
}

func (s ScreenBundle) captureRegionContext(ctx context.Context, rect image.Rectangle, path string) error {
	s.traceAction(fmt.Sprintf("capture-region rect=%s path=%q", rect, path))
	img, err := s.grabContext(ctx, rect)
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
	return s.getPixelContext(context.Background(), x, y)
}

func (s ScreenBundle) getPixelContext(ctx context.Context, x, y int) (color.RGBA, error) {
	s.traceAction(fmt.Sprintf("get-pixel x=%d y=%d", x, y))
	if err := s.checkAvailable(); err != nil {
		return color.RGBA{}, err
	}
	c, err := find.FirstPixel(ctx, s.Screenshotter, image.Rect(x, y, x+1, y+1))
	if err != nil {
		return color.RGBA{}, err
	}
	return c, nil
}

func (s ScreenBundle) getMultiplePixels(points []image.Point) ([]color.RGBA, error) {
	return s.getMultiplePixelsContext(context.Background(), points)
}

func (s ScreenBundle) getMultiplePixelsContext(ctx context.Context, points []image.Point) ([]color.RGBA, error) {
	s.traceAction(fmt.Sprintf("get-multiple-pixels count=%d", len(points)))
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	out := make([]color.RGBA, len(points))
	if len(points) == 0 {
		return out, nil
	}
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
	img, err := s.grabContext(ctx, bounds)
	if err != nil {
		return nil, err
	}
	for i, p := range points {
		c := color.RGBAModel.Convert(img.At(p.X, p.Y)).(color.RGBA)
		out[i] = c
	}
	return out, nil
}

// pixelToScreen currently returns the rectangle's Min point. If a real
// coordinate conversion is required by some backends, implement it here.
// TODO: remove this helper if unused or implement proper conversion.
func (s ScreenBundle) pixelToScreen(rect image.Rectangle) (int, int, error) {
	return rect.Min.X, rect.Min.Y, nil
}

func (s ScreenBundle) waitForFn(rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	return s.waitForFnContext(context.Background(), rect, fn, poll)
}

func (s ScreenBundle) WaitForAnyChange(timeout time.Duration) {
	_ = s.waitForAnyChangeContext(context.Background(), timeout)
}

func (s ScreenBundle) waitForAnyChangeContext(ctx context.Context, timeout time.Duration) error {
	s.traceAction(fmt.Sprintf("wait-for-any-change timeout=%s", timeout))
	if err := s.checkAvailable(); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, _ = s.waitForVisibleChangeContext(cctx, image.Rectangle{}, 0, 1)
	return nil
}

func (s ScreenBundle) WaitForStableOrTimeout(rect image.Rectangle, timeout time.Duration) {
	_ = s.waitForStableOrTimeoutContext(context.Background(), rect, timeout)
}

func (s ScreenBundle) waitForStableOrTimeoutContext(ctx context.Context, rect image.Rectangle, timeout time.Duration) error {
	s.traceAction(fmt.Sprintf("wait-for-stable-or-timeout rect=%s timeout=%s", rect, timeout))
	if err := s.checkAvailable(); err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, _ = find.WaitForNoChange(cctx, s.Screenshotter, rect, 3, 0, nil)
	return nil
}

func (s ScreenBundle) waitForFnContext(ctx context.Context, rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	s.traceAction(fmt.Sprintf("wait-for-fn rect=%s poll=%s", rect, poll))
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	return find.WaitForFn(ctx, s.Screenshotter, rect, fn, poll)
}

func (s ScreenBundle) WaitForVisibleChange(rect image.Rectangle, poll time.Duration, stable int) (uint32, error) {
	return s.waitForVisibleChangeContext(context.Background(), rect, poll, stable)
}

func (s ScreenBundle) waitForVisibleChangeContext(ctx context.Context, rect image.Rectangle, poll time.Duration, stable int) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for-visible-change rect=%s poll=%s stable=%d", rect, poll, stable))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	initial, err := s.grabHashContext(ctx, rect)
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
	return s.waitForStableContext(context.Background(), rect, stableN, poll)
}

func (s ScreenBundle) waitForStableContext(ctx context.Context, rect image.Rectangle, stableN int, poll time.Duration) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for-stable rect=%s stable=%d poll=%s", rect, stableN, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stableN, poll, nil)
}

func (s ScreenBundle) waitForSettle(rect image.Rectangle, action func(), stable int, poll time.Duration) (uint32, error) {
	return s.waitForSettleContext(context.Background(), rect, action, stable, poll)
}

func (s ScreenBundle) waitForSettleContext(ctx context.Context, rect image.Rectangle, action func(), stable int, poll time.Duration) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for-settle rect=%s stable=%d poll=%s", rect, stable, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	before, err := s.grabHashContext(ctx, rect)
	if err != nil {
		return 0, err
	}
	action()
	if _, err := find.WaitForChange(ctx, s.Screenshotter, rect, before, poll, nil); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

func (s ScreenBundle) locateExact(rect image.Rectangle, reference image.Image) (image.Rectangle, error) {
	return s.locateExactContext(context.Background(), rect, reference)
}

func (s ScreenBundle) locateExactContext(ctx context.Context, searchArea image.Rectangle, reference image.Image) (image.Rectangle, error) {
	s.traceAction(fmt.Sprintf("locate-exact search=%s", searchArea))
	if err := s.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	return find.LocateExact(ctx, s.Screenshotter, searchArea, reference)
}

func (s ScreenBundle) waitForLocate(rect image.Rectangle, reference image.Image, poll time.Duration) (image.Rectangle, error) {
	return s.waitForLocateContext(context.Background(), rect, reference, poll)
}

func (s ScreenBundle) waitForLocateContext(ctx context.Context, searchArea image.Rectangle, reference image.Image, poll time.Duration) (image.Rectangle, error) {
	s.traceAction(fmt.Sprintf("wait-for-locate search=%s poll=%s", searchArea, poll))
	if err := s.checkAvailable(); err != nil {
		return image.Rectangle{}, err
	}
	return find.WaitForLocate(ctx, s.Screenshotter, searchArea, reference, poll)
}

func (s ScreenBundle) waitWithTolerance(rect image.Rectangle, reference image.Image, radius int, poll time.Duration) (uint32, image.Rectangle, error) {
	return s.waitWithToleranceContext(context.Background(), rect, reference, radius, poll)
}

func (s ScreenBundle) waitWithToleranceContext(ctx context.Context, rect image.Rectangle, reference image.Image, radius int, poll time.Duration) (uint32, image.Rectangle, error) {
	s.traceAction(fmt.Sprintf("wait-with-tolerance rect=%s radius=%d poll=%s", rect, radius, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, image.Rectangle{}, err
	}
	return find.WaitWithTolerance(ctx, s.Screenshotter, rect, reference, radius, poll, nil)
}

func (s ScreenBundle) FindColor(rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	return s.findColorContext(context.Background(), rect, target, tolerance)
}

func (s ScreenBundle) findColorContext(ctx context.Context, rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	s.traceAction(fmt.Sprintf("find-color rect=%s tolerance=%d", rect, tolerance))
	if err := s.checkAvailable(); err != nil {
		return image.Point{}, err
	}
	return find.FindColor(ctx, s.Screenshotter, rect, target, tolerance)
}

func (s ScreenBundle) WaitForNoChange(rect image.Rectangle, stable int, poll time.Duration) (uint32, error) {
	return s.waitForNoChangeContext(context.Background(), rect, stable, poll)
}

func (s ScreenBundle) waitForNoChangeContext(ctx context.Context, rect image.Rectangle, stable int, poll time.Duration) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for-no-change rect=%s stable=%d poll=%s", rect, stable, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

func (s ScreenBundle) WaitForChange(rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	return s.waitForChangeContext(context.Background(), rect, initial, poll)
}

func (s ScreenBundle) waitForChangeContext(ctx context.Context, rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for-change rect=%s initial=%08x poll=%s", rect, initial, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
}

func (s ScreenBundle) WaitFor(rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	return s.waitForContext(context.Background(), rect, want, poll)
}

func (s ScreenBundle) waitForContext(ctx context.Context, rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for rect=%s want=%08x poll=%s", rect, want, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitFor(ctx, s.Screenshotter, rect, want, poll, nil)
}

func (s ScreenBundle) scanFor(rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	return s.scanForContext(context.Background(), rects, wants, poll)
}

func (s ScreenBundle) scanForContext(ctx context.Context, rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	s.traceAction(fmt.Sprintf("scan-for rects=%d wants=%d poll=%s", len(rects), len(wants), poll))
	if err := s.checkAvailable(); err != nil {
		return find.Result{}, err
	}
	return find.ScanFor(ctx, s.Screenshotter, rects, wants, poll, nil)
}

func (s ScreenBundle) Resolution() (int, int, error) {
	return s.resolutionContext(context.Background())
}

func (s ScreenBundle) resolutionContext(ctx context.Context) (int, int, error) {
	s.traceAction("resolution")
	if err := s.checkAvailable(); err != nil {
		return 0, 0, err
	}
	return screen.ResolutionWithContext(ctx, s.Screenshotter)
}
