package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
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

func (s *ScreenBundle) checkAvailable(operation string) error {
	if s == nil {
		return (&bundleBase{}).unavailable(operation)
	}
	return s.bundleBase.checkAvailable(operation, s.backend != nil)
}

func (s *ScreenBundle) grabHash(
	ctx context.Context,
	rect image.Rectangle,
) (uint32, error) {
	s.traceAction("screen", "grab-hash rect=%s", rect)
	if err := s.checkAvailable("hash"); err != nil {
		return 0, err
	}
	hash, err := find.GrabHash(ctx, s.backend, rect, nil)
	return hash, s.operationError("hash", err)
}

func (s *ScreenBundle) grab(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	s.traceAction("screen", "grab rect=%s", rect)
	if err := s.checkAvailable("capture"); err != nil {
		return nil, err
	}
	img, err := s.backend.Grab(ctx, rect)
	return img, s.operationError("capture", err)
}

// Grab captures rect, or the full screen when rect is empty.
func (s *ScreenBundle) Grab(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	return s.grab(ctx, rect)
}

// GrabFullHash returns a hash of the full screen.
func (s *ScreenBundle) GrabFullHash(ctx context.Context) (uint32, error) {
	return s.grabHash(ctx, image.Rectangle{})
}

// GrabRegionHash returns a hash of rect.
func (s *ScreenBundle) GrabRegionHash(
	ctx context.Context,
	rect image.Rectangle,
) (uint32, error) {
	return s.grabHash(ctx, rect)
}

// GetAllPixels captures the full screen.
func (s *ScreenBundle) GetAllPixels(ctx context.Context) (image.Image, error) {
	s.traceAction("screen", "get-all-pixels")
	return s.grab(ctx, image.Rectangle{})
}

// GrabRegion captures rect.
func (s *ScreenBundle) GrabRegion(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	s.traceAction("screen", "grab-region rect=%s", rect)
	return s.grab(ctx, rect)
}

// CaptureRegion captures rect and writes it as a PNG to path.
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

// GetPixel returns the pixel at x and y.
func (s *ScreenBundle) GetPixel(
	ctx context.Context,
	x int,
	y int,
) (color.RGBA, error) {
	s.traceAction("screen", "get-pixel x=%d y=%d", x, y)
	if err := s.checkAvailable("pixel"); err != nil {
		return color.RGBA{}, err
	}
	if x == math.MaxInt || y == math.MaxInt {
		return color.RGBA{}, s.operationError("pixel", fmt.Errorf("screen: pixel coordinate overflows one-pixel capture: (%d,%d)", x, y))
	}
	pixel, err := find.FirstPixel(
		ctx,
		s.backend,
		image.Rect(x, y, x+1, y+1),
	)
	if err != nil {
		return color.RGBA{}, s.operationError("pixel", err)
	}
	return pixel, nil
}

// GetMultiplePixels returns pixels at points in the same order.
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
		if point.X == math.MaxInt || point.Y == math.MaxInt {
			return nil, s.operationError("pixel", fmt.Errorf("screen: pixel coordinate overflows capture bounds: %v", point))
		}
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
	if img == nil {
		return nil, s.operationError("pixel", errors.New("screen: capture returned nil image"))
	}
	imgBounds := img.Bounds()
	for i, point := range points {
		imagePoint := translatePointToBounds(
			point,
			bounds.Min,
			imgBounds.Min,
		)
		if !imagePoint.In(imgBounds) {
			return nil, s.operationError(
				"pixel",
				fmt.Errorf("screen: capture bounds %v do not contain requested point %v", imgBounds, point),
			)
		}
		rgba, ok := color.RGBAModel.Convert(
			img.At(imagePoint.X, imagePoint.Y),
		).(color.RGBA)
		if !ok {
			return nil, errors.New("screen: RGBA conversion returned unexpected type")
		}
		out[i] = rgba
	}
	return out, nil
}

// WaitForFn waits for fn to accept a captured image.
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
	img, err := find.WaitForFn(ctx, s.backend, rect, fn, poll)
	return img, s.operationError("wait", err)
}

// WaitForSettle runs action and waits for the region to stabilize.
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
		if actionErr := action(); actionErr != nil {
			return 0, actionErr
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
		return 0, s.operationError("wait-change", err)
	}
	stableHash, err := find.WaitForNoChangeFrom(
		ctx,
		s.backend,
		rect,
		changed,
		stable,
		poll,
		nil,
	)
	return stableHash, s.operationError("wait-stable", err)
}

func translatePointToBounds(
	point image.Point,
	fromMin image.Point,
	toMin image.Point,
) image.Point {
	return point.Add(toMin.Sub(fromMin))
}

// WaitForNoChange waits until rect remains unchanged for stable samples.
func (s *ScreenBundle) WaitForNoChange(
	ctx context.Context,
	rect image.Rectangle,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	hash, err := find.WaitForNoChange(
		ctx,
		s.backend,
		rect,
		stable,
		poll,
		nil,
	)
	return hash, s.operationError("wait-stable", err)
}

// WaitForNoChangeFrom waits for rect to remain unchanged from initial.
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
	hash, err := find.WaitForNoChangeFrom(
		ctx,
		s.backend,
		rect,
		initial,
		stable,
		poll,
		nil,
	)
	return hash, s.operationError("wait-stable", err)
}

// FindColor returns the first pixel in rect within tolerance of target.
func (s *ScreenBundle) FindColor(
	ctx context.Context,
	rect image.Rectangle,
	target color.RGBA,
	tolerance int,
) (image.Point, error) {
	if err := s.checkAvailable("pixel"); err != nil {
		return image.Point{}, err
	}
	point, err := find.FindColor(ctx, s.backend, rect, target, tolerance)
	return point, s.operationError("pixel", err)
}

// WaitForChange waits until rect differs from initial.
func (s *ScreenBundle) WaitForChange(
	ctx context.Context,
	rect image.Rectangle,
	initial uint32,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-change"); err != nil {
		return 0, err
	}
	hash, err := find.WaitForChange(
		ctx,
		s.backend,
		rect,
		initial,
		poll,
		nil,
	)
	return hash, s.operationError("wait-change", err)
}

// WaitFor waits until rect has the requested hash.
func (s *ScreenBundle) WaitFor(
	ctx context.Context,
	rect image.Rectangle,
	want uint32,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return 0, err
	}
	hash, err := find.WaitFor(ctx, s.backend, rect, want, poll, nil)
	return hash, s.operationError("wait", err)
}

// ScanFor waits for any requested hash across the supplied rectangles.
func (s *ScreenBundle) ScanFor(
	ctx context.Context,
	rects []image.Rectangle,
	wants []uint32,
	poll time.Duration,
) (find.Result, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return find.Result{}, err
	}
	result, err := find.ScanFor(ctx, s.backend, rects, wants, poll, nil)
	return result, s.operationError("wait", err)
}

// Resolution returns the active capture resolution.
func (s *ScreenBundle) Resolution(ctx context.Context) (int, int, error) {
	if err := s.checkAvailable("resolution"); err != nil {
		return 0, 0, err
	}
	width, height, err := screen.ResolutionWithContext(ctx, s.backend)
	return width, height, s.operationError("resolution", err)
}
