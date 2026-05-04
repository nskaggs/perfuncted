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

func (s ScreenBundle) grabHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	s.traceAction(fmt.Sprintf("grab-hash rect=%s", rect))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	if rect.Empty() {
		return s.Screenshotter.GrabFullHash(ctx)
	}
	return find.GrabHash(ctx, s.Screenshotter, rect, nil)
}

func (s ScreenBundle) grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.traceAction(fmt.Sprintf("grab rect=%s", rect))
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	if rect.Empty() {
		return s.Screenshotter.Grab(ctx, image.Rectangle{})
	}
	return s.Screenshotter.Grab(ctx, rect)
}

// CaptureRegion captures the given screen region and saves it as a PNG file at path.
func (s ScreenBundle) CaptureRegion(rect image.Rectangle, path string) error {
	s.traceAction(fmt.Sprintf("capture-region rect=%s path=%q", rect, path))
	img, err := s.grab(context.Background(), rect)
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

// GetPixel returns the colour of the pixel at (x, y).
func (s ScreenBundle) GetPixel(x, y int) (color.RGBA, error) {
	s.traceAction(fmt.Sprintf("get-pixel x=%d y=%d", x, y))
	if err := s.checkAvailable(); err != nil {
		return color.RGBA{}, err
	}
	c, err := find.FirstPixel(context.Background(), s.Screenshotter, image.Rect(x, y, x+1, y+1))
	if err != nil {
		return color.RGBA{}, err
	}
	return c, nil
}

// GetMultiplePixels returns the colours of all given points in a single grab.
func (s ScreenBundle) GetMultiplePixels(points []image.Point) ([]color.RGBA, error) {
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
	img, err := s.grab(context.Background(), bounds)
	if err != nil {
		return nil, err
	}
	for i, p := range points {
		c := color.RGBAModel.Convert(img.At(p.X, p.Y)).(color.RGBA)
		out[i] = c
	}
	return out, nil
}

// WaitForFn polls rect every poll interval until fn returns true, or ctx is cancelled.
func (s ScreenBundle) WaitForFn(ctx context.Context, rect image.Rectangle, fn func(image.Image) bool, poll time.Duration) (image.Image, error) {
	s.traceAction(fmt.Sprintf("wait-for-fn rect=%s poll=%s", rect, poll))
	if err := s.checkAvailable(); err != nil {
		return nil, err
	}
	return find.WaitForFn(ctx, s.Screenshotter, rect, fn, poll)
}

// WaitForSettle grabs a hash before calling action, waits for the region to
// change, then waits for it to stabilise over stable consecutive polls.
func (s ScreenBundle) WaitForSettle(ctx context.Context, rect image.Rectangle, action func(), stable int, poll time.Duration) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for-settle rect=%s stable=%d poll=%s", rect, stable, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	before, err := s.grabHash(ctx, rect)
	if err != nil {
		return 0, err
	}
	action()
	if _, err := find.WaitForChange(ctx, s.Screenshotter, rect, before, poll, nil); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(ctx, s.Screenshotter, rect, stable, poll, nil)
}

// FindColor searches rect for the first pixel matching target within tolerance.
func (s ScreenBundle) FindColor(rect image.Rectangle, target color.RGBA, tolerance int) (image.Point, error) {
	s.traceAction(fmt.Sprintf("find-color rect=%s tolerance=%d", rect, tolerance))
	if err := s.checkAvailable(); err != nil {
		return image.Point{}, err
	}
	return find.FindColor(context.Background(), s.Screenshotter, rect, target, tolerance)
}

// WaitForChange polls rect every poll interval until its hash differs from
// initial, or ctx is cancelled.
func (s ScreenBundle) WaitForChange(ctx context.Context, rect image.Rectangle, initial uint32, poll time.Duration) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for-change rect=%s initial=%08x poll=%s", rect, initial, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitForChange(ctx, s.Screenshotter, rect, initial, poll, nil)
}

// WaitFor polls rect every poll interval until its hash equals want, or ctx
// is cancelled.
func (s ScreenBundle) WaitFor(ctx context.Context, rect image.Rectangle, want uint32, poll time.Duration) (uint32, error) {
	s.traceAction(fmt.Sprintf("wait-for rect=%s want=%08x poll=%s", rect, want, poll))
	if err := s.checkAvailable(); err != nil {
		return 0, err
	}
	return find.WaitFor(ctx, s.Screenshotter, rect, want, poll, nil)
}

// ScanFor polls multiple regions in round-robin until one matches its
// expected hash, or ctx is cancelled.
func (s ScreenBundle) ScanFor(ctx context.Context, rects []image.Rectangle, wants []uint32, poll time.Duration) (find.Result, error) {
	s.traceAction(fmt.Sprintf("scan-for rects=%d wants=%d poll=%s", len(rects), len(wants), poll))
	if err := s.checkAvailable(); err != nil {
		return find.Result{}, err
	}
	return find.ScanFor(ctx, s.Screenshotter, rects, wants, poll, nil)
}

// Resolution returns the current screen dimensions.
func (s ScreenBundle) Resolution() (int, int, error) {
	s.traceAction("resolution")
	if err := s.checkAvailable(); err != nil {
		return 0, 0, err
	}
	return screen.ResolutionWithContext(context.Background(), s.Screenshotter)
}
