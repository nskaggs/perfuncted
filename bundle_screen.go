package perfuncted

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/screen"
)

type ScreenBundle struct {
	screen.Screenshotter
	bundleBase
}

func (s ScreenBundle) close() error {
	if s.Screenshotter == nil {
		return nil
	}
	s.traceAction("screen", "close")
	return s.Screenshotter.Close()
}

func (s ScreenBundle) checkAvailable() error {
	return checkAvailable(s.Screenshotter, "screen")
}

func (s ScreenBundle) grabHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	s.traceAction("screen", "grab-hash rect=%s", rect)
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	if rect.Empty() {
		return s.Screenshotter.GrabFullHash(ctx)
	}
	return find.GrabHash(ctx, s.Screenshotter, rect, nil)
}

func (s ScreenBundle) grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.traceAction("screen", "grab rect=%s", rect)
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	if rect.Empty() {
		return s.Screenshotter.Grab(ctx, image.Rectangle{})
	}
	return s.Screenshotter.Grab(ctx, rect)
}

func (s ScreenBundle) GetAllPixels(ctx context.Context) (image.Image, error) {
	s.traceAction("screen", "get-all-pixels")
	return s.grab(ctx, image.Rectangle{})
}

func (s ScreenBundle) GrabRegion(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.traceAction("screen", "grab-region rect=%s", rect)
	return s.grab(ctx, rect)
}

func (s ScreenBundle) CaptureRegion(ctx context.Context, rect image.Rectangle, path string) error {
	s.traceAction("screen", "capture-region rect=%s path=%q", rect, path)
	img, err := s.grab(ctx, rect)
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

func (s ScreenBundle) GetPixel(ctx context.Context, x, y int) (color.RGBA, error) {
	s.traceAction("screen", "get-pixel x=%d y=%d", x, y)
	if err := s.checkAvailable(); err != nil {
		return color.RGBA{}, err
	}
	c, err := find.FirstPixel(ctx, s.Screenshotter, image.Rect(x, y, x+1, y+1))
	if err != nil {
		return color.RGBA{}, err
	}
	return c, nil
}

func (s ScreenBundle) GetMultiplePixels(ctx context.Context, points []image.Point) ([]color.RGBA, error) {
	s.traceAction("screen", "get-multiple-pixels count=%d", len(points))
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
	img, err := s.grab(ctx, bounds)
	if err != nil {
		return nil, err
	}
	for i, p := range points {
		ip := translatePointToBounds(p, bounds.Min, img.Bounds().Min)
		c := color.RGBAModel.Convert(img.At(ip.X, ip.Y)).(color.RGBA)
		out[i] = c
	}
	return out, nil
}

func (s ScreenBundle) WaitForFn(ctx context.Context, rect image.Rectangle, fn func(context.Context, image.Image) bool, poll time.Duration) (image.Image, error) {
	s.traceAction("screen", "wait-for-fn rect=%s poll=%s", rect, poll)
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	return find.WaitForFn(ctx, s.Screenshotter, rect, fn, poll)
}

func (s ScreenBundle) WaitForSettle(ctx context.Context, rect image.Rectangle, action func() error, stable int, poll time.Duration) (uint32, error) {
	s.traceAction("screen", "wait-for-settle rect=%s stable=%d poll=%s", rect, stable, poll)
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	before, err := s.grabHash(ctx, rect)
	if err != nil {
		return 0, err
	}
	if action != nil {
		actionErr := action()
		if actionErr != nil {
			return 0, actionErr
		}
	}
	changed, err := find.WaitForChange(ctx, s.Screenshotter, rect, before, poll, nil)
	if err != nil {
		return 0, err
	}
	return find.WaitForNoChangeFrom(ctx, s.Screenshotter, rect, changed, stable, poll, nil)
}

func translatePointToBounds(p, fromMin, toMin image.Point) image.Point {
	return p.Add(toMin.Sub(fromMin))
}

func (s ScreenBundle) WaitForNoChange(ctx context.Context, rect image.Rectangle, stable int, poll time.Duration) (uint32, error) {
	s.traceAction("screen", "wait-for-no-change rect=%s stable=%d poll=%s", rect, stable, poll)
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

func (s ScreenBundle) WaitForNoChangeFrom(ctx context.Context, rect image.Rectangle, initial uint32, stable int, poll time.Duration) (uint32, error) {
	s.traceAction("screen", "wait-for-no-change-from rect=%s initial=%08x stable=%d poll=%s", rect, initial, stable, poll)
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForNoChangeFrom(ctx, s.Screenshotter, rect, initial, stable, poll, nil)
}

func (s ScreenBundle) FindColor(ctx context.Context, rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	s.traceAction("screen", "find-color rect=%s tolerance=%d", rect, tolerance)
	if err := s.checkAvailable(); err != nil {
		return image.Point{}, err
	}
	return find.FindColor(ctx, s.Screenshotter, rect, target, tolerance)
}

func (s ScreenBundle) WaitForChange(ctx context.Context, rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	s.traceAction("screen", "wait-for-change rect=%s initial=%08x poll=%s", rect, initial, poll)
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
}

func (s ScreenBundle) WaitFor(ctx context.Context, rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	s.traceAction("screen", "wait-for rect=%s want=%08x poll=%s", rect, want, poll)
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitFor(ctx, s.Screenshotter, rect, want, poll, nil)
}

func (s ScreenBundle) ScanFor(ctx context.Context, rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	s.traceAction("screen", "scan-for rects=%d wants=%d poll=%s", len(rects), len(wants), poll)
	if err := s.checkAvailable(); err != nil {
		return find.Result{}, err
	}
	return find.ScanFor(ctx, s.Screenshotter, rects, wants, poll, nil)
}

func (s ScreenBundle) Resolution(ctx context.Context) (int, int, error) {
	s.traceAction("screen", "resolution")
	if err := s.checkAvailable(); err != nil {
		return 0, 0, err
	}
	return screen.ResolutionWithContext(ctx, s.Screenshotter)
}
