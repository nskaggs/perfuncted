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

// ScreenBundle exposes screen operations without exposing an absent backend to
// callers. A Session always initializes this facade.
type ScreenBundle struct {
	backend screen.Screenshotter
	bundleBase
}

func (s *ScreenBundle) close() error {
	if s == nil || s.backend == nil {
		return nil
	}
	s.traceAction("screen", "close")
	return s.backend.Close()
}

func (s *ScreenBundle) checkAvailable(operation string) error {
	if s == nil || s.backend == nil {
		return s.unavailable(operation)
	}
	return nil
}

func (s *ScreenBundle) grabHash(
	ctx context.Context,
	rect image.Rectangle,
) (uint32, error) {
	s.traceAction("screen", "grab-hash rect=%s", rect)
	if err := s.checkAvailable("hash"); err != nil {
		return 0, err
	}
	if rect.Empty() {
		return s.backend.GrabFullHash(ctx)
	}
	return find.GrabHash(ctx, s.backend, rect, nil)
}

func (s *ScreenBundle) grab(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	s.traceAction("screen", "grab rect=%s", rect)
	if err := s.checkAvailable("capture"); err != nil {
		return nil, err
	}
	return s.backend.Grab(ctx, rect)
}

func (s *ScreenBundle) Grab(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	return s.grab(ctx, rect)
}

func (s *ScreenBundle) GrabFullHash(ctx context.Context) (uint32, error) {
	return s.grabHash(ctx, image.Rectangle{})
}

func (s *ScreenBundle) GrabRegionHash(
	ctx context.Context,
	rect image.Rectangle,
) (uint32, error) {
	return s.grabHash(ctx, rect)
}

func (s *ScreenBundle) GetAllPixels(ctx context.Context) (image.Image, error) {
	s.traceAction("screen", "get-all-pixels")
	return s.grab(ctx, image.Rectangle{})
}

func (s *ScreenBundle) GrabRegion(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	s.traceAction("screen", "grab-region rect=%s", rect)
	return s.grab(ctx, rect)
}

func (s *ScreenBundle) CaptureRegion(
	ctx context.Context,
	rect image.Rectangle,
	path string,
) error {
	s.traceAction("screen", "capture-region rect=%s path=%q", rect, path)
	img, err := s.grab(ctx, rect)
	if err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}

func (s *ScreenBundle) GetPixel(
	ctx context.Context,
	x int,
	y int,
) (color.RGBA, error) {
	s.traceAction("screen", "get-pixel x=%d y=%d", x, y)
	if err := s.checkAvailable("pixel"); err != nil {
		return color.RGBA{}, err
	}
	pixel, err := find.FirstPixel(
		ctx,
		s.backend,
		image.Rect(x, y, x+1, y+1),
	)
	if err != nil {
		return color.RGBA{}, err
	}
	return pixel, nil
}

func (s *ScreenBundle) GetMultiplePixels(
	ctx context.Context,
	points []image.Point,
) ([]color.RGBA, error) {
	s.traceAction("screen", "get-multiple-pixels count=%d", len(points))
	if err := s.checkAvailable("pixel"); err != nil {
		return nil, err
	}
	out := make([]color.RGBA, len(points))
	if len(points) == 0 {
		return out, nil
	}

	minX, minY := points[0].X, points[0].Y
	maxX, maxY := minX, minY
	for _, point := range points {
		if point.X < minX {
			minX = point.X
		}
		if point.Y < minY {
			minY = point.Y
		}
		if point.X > maxX {
			maxX = point.X
		}
		if point.Y > maxY {
			maxY = point.Y
		}
	}
	bounds := image.Rect(minX, minY, maxX+1, maxY+1)
	img, err := s.grab(ctx, bounds)
	if err != nil {
		return nil, err
	}
	for i, point := range points {
		imagePoint := translatePointToBounds(
			point,
			bounds.Min,
			img.Bounds().Min,
		)
		out[i] = color.RGBAModel.Convert(
			img.At(imagePoint.X, imagePoint.Y),
		).(color.RGBA)
	}
	return out, nil
}

func (s *ScreenBundle) WaitForFn(
	ctx context.Context,
	rect image.Rectangle,
	fn func(context.Context, image.Image) bool,
	poll time.Duration,
) (image.Image, error) {
	s.traceAction("screen", "wait-for-fn rect=%s poll=%s", rect, poll)
	if err := s.checkAvailable("wait"); err != nil {
		return nil, err
	}
	return find.WaitForFn(ctx, s.backend, rect, fn, poll)
}

func (s *ScreenBundle) WaitForSettle(
	ctx context.Context,
	rect image.Rectangle,
	action func() error,
	stable int,
	poll time.Duration,
) (uint32, error) {
	s.traceAction(
		"screen",
		"wait-for-settle rect=%s stable=%d poll=%s",
		rect,
		stable,
		poll,
	)
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	before, err := s.grabHash(ctx, rect)
	if err != nil {
		return 0, err
	}
	if action != nil {
		if err := action(); err != nil {
			return 0, err
		}
	}
	changed, err := find.WaitForChange(
		ctx,
		s.backend,
		rect,
		before,
		poll,
		nil,
	)
	if err != nil {
		return 0, err
	}
	return find.WaitForNoChangeFrom(
		ctx,
		s.backend,
		rect,
		changed,
		stable,
		poll,
		nil,
	)
}

func translatePointToBounds(
	point image.Point,
	fromMin image.Point,
	toMin image.Point,
) image.Point {
	return point.Add(toMin.Sub(fromMin))
}

func (s *ScreenBundle) WaitForNoChange(
	ctx context.Context,
	rect image.Rectangle,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	return find.WaitForNoChange(
		ctx,
		s.backend,
		rect,
		stable,
		poll,
		nil,
	)
}

func (s *ScreenBundle) WaitForNoChangeFrom(
	ctx context.Context,
	rect image.Rectangle,
	initial uint32,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	return find.WaitForNoChangeFrom(
		ctx,
		s.backend,
		rect,
		initial,
		stable,
		poll,
		nil,
	)
}

func (s *ScreenBundle) FindColor(
	ctx context.Context,
	rect image.Rectangle,
	target color.RGBA,
	tolerance int,
) (image.Point, error) {
	if err := s.checkAvailable("pixel"); err != nil {
		return image.Point{}, err
	}
	return find.FindColor(ctx, s.backend, rect, target, tolerance)
}

func (s *ScreenBundle) WaitForChange(
	ctx context.Context,
	rect image.Rectangle,
	initial uint32,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-change"); err != nil {
		return 0, err
	}
	return find.WaitForChange(
		ctx,
		s.backend,
		rect,
		initial,
		poll,
		nil,
	)
}

func (s *ScreenBundle) WaitFor(
	ctx context.Context,
	rect image.Rectangle,
	want uint32,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return 0, err
	}
	return find.WaitFor(ctx, s.backend, rect, want, poll, nil)
}

func (s *ScreenBundle) ScanFor(
	ctx context.Context,
	rects []image.Rectangle,
	wants []uint32,
	poll time.Duration,
) (find.Result, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return find.Result{}, err
	}
	return find.ScanFor(ctx, s.backend, rects, wants, poll, nil)
}

func (s *ScreenBundle) Resolution(ctx context.Context) (int, int, error) {
	if err := s.checkAvailable("resolution"); err != nil {
		return 0, 0, err
	}
	return screen.ResolutionWithContext(ctx, s.backend)
}
