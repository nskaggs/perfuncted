package sessionscreen

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"github.com/nskaggs/perfuncted/v2/internal/capture"
	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"github.com/nskaggs/perfuncted/v2/screen"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/bits"
	"os"
	"path/filepath"
	"time"
)

// ScreenBundle exposes screen operations without exposing an absent backend to
// callers. A Session always initializes this facade.
type state struct {
	backend screen.Screenshotter
	gate    Admission
}

// traceAction shadows the promoted bundleBase method so a nil bundle is handled
// before it is dereferenced.
//
// bundleBase is embedded by value, so the promoted method's receiver is
// &s.bundleBase: calling it on a nil *ScreenBundle dereferences s to compute the
// receiver and panics, before bundleBase's own nil check can run. Every traced
// entry point therefore has to tolerate a nil receiver the way checkAvailable
// does.
func (s *state) traceAction(format string, args ...any) {
	if s == nil {
		return
	}
	if s.gate != nil {
		s.gate.Tracef("screen", format, args...)
	}
}

func (s *state) checkAvailable(operation string) error {
	if s == nil {
		return capability.ErrNilSession
	}
	if s.gate == nil {
		return capability.ErrNilSession
	}
	return s.gate.CheckBackend(operation, s.backend)
}

func (s *state) close() error {
	if s == nil {
		return nil
	}
	if util.IsNil(s.backend) {
		return nil
	}
	return s.backend.Close()
}

func (s *state) grabHash(
	ctx context.Context,
	rect image.Rectangle,
) (uint32, error) {
	if err := s.checkAvailable("hash"); err != nil {
		return 0, err
	}
	s.traceAction("grab-hash rect=%s", rect)
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	hash, err := find.GrabHash(ctx, s.backend, rect, nil)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if openErr := s.gate.EnsureOpen(); openErr != nil {
		return 0, openErr
	}
	return hash, s.gate.OperationError("hash", err)
}

func (s *state) grab(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	if err := s.checkAvailable("capture"); err != nil {
		return nil, err
	}
	s.traceAction("grab rect=%s", rect)
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	img, err := s.backend.Grab(ctx, rect)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if openErr := s.gate.EnsureOpen(); openErr != nil {
		return nil, openErr
	}
	if err != nil {
		return nil, s.gate.OperationError("capture", err)
	}
	if err := capture.ValidateImageLayout(img); err != nil {
		return nil, s.gate.OperationError("capture", err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return img, nil
}

// Grab captures rect, or the full screen when rect is empty.
func (s *state) Grab(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	return s.grab(ctx, rect)
}

// GrabFullHash returns a hash of the full screen.
func (s *state) GrabFullHash(ctx context.Context) (uint32, error) {
	return s.grabHash(ctx, image.Rectangle{})
}

// GrabRegionHash returns a hash of rect.
func (s *state) GrabRegionHash(
	ctx context.Context,
	rect image.Rectangle,
) (uint32, error) {
	return s.grabHash(ctx, rect)
}

// GetAllPixels captures the full screen.
func (s *state) GetAllPixels(ctx context.Context) (image.Image, error) {
	s.traceAction("get-all-pixels")
	return s.grab(ctx, image.Rectangle{})
}

// GrabRegion captures rect.
func (s *state) GrabRegion(
	ctx context.Context,
	rect image.Rectangle,
) (image.Image, error) {
	s.traceAction("grab-region rect=%s", rect)
	return s.grab(ctx, rect)
}

// CaptureRegion captures rect and writes it as a PNG to path.
func (s *state) CaptureRegion(
	ctx context.Context,
	rect image.Rectangle,
	path string,
) error {
	ctx = contextutil.Default(ctx)
	s.traceAction("capture-region rect=%s path=%q", rect, path)
	img, err := s.grab(ctx, rect)
	if err != nil {
		return err
	}
	return s.savePNG(ctx, img, path)
}

func (s *state) savePNG(ctx context.Context, img image.Image, path string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if util.IsNil(img) {
		return s.gate.OperationError("capture", fmt.Errorf("screen: capture returned nil image"))
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".perfuncted-capture-*")
	if err != nil {
		return s.gate.OperationError("capture", fmt.Errorf("screen: create %q: %w", path, err))
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	encodeErr := png.Encode(file, img)
	closeErr := file.Close()
	if encodeErr != nil && closeErr != nil {
		return s.gate.OperationError(
			"capture",
			errors.Join(encodeErr, fmt.Errorf("screen: close %q: %w", path, closeErr)),
		)
	}
	if encodeErr != nil {
		return s.gate.OperationError("capture", encodeErr)
	}
	if closeErr != nil {
		return s.gate.OperationError("capture", fmt.Errorf("screen: close %q: %w", path, closeErr))
	}
	// Encoding may call an image provider after the capture has completed.
	// Cancellation or Session.Close during that work revokes permission to
	// publish the resulting screenshot, preserving any existing destination.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.gate.EnsureOpen(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return s.gate.OperationError("capture", fmt.Errorf("screen: publish %q: %w", path, err))
	}
	return nil
}

// GetPixel returns the pixel at x and y.
func (s *state) GetPixel(
	ctx context.Context,
	x int,
	y int,
) (color.RGBA, error) {
	if err := s.checkAvailable("pixel"); err != nil {
		return color.RGBA{}, err
	}
	s.traceAction("get-pixel x=%d y=%d", x, y)
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	if x == math.MaxInt || y == math.MaxInt {
		return color.RGBA{}, s.gate.OperationError("pixel", fmt.Errorf("screen: pixel coordinate overflows one-pixel capture: (%d,%d)", x, y))
	}
	pixel, err := find.FirstPixel(
		ctx,
		s.backend,
		image.Rect(x, y, x+1, y+1),
	)
	if err != nil {
		return color.RGBA{}, s.gate.OperationError("pixel", err)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return color.RGBA{}, ctxErr
	}
	if openErr := s.gate.EnsureOpen(); openErr != nil {
		return color.RGBA{}, openErr
	}
	return pixel, nil
}

// GetMultiplePixels returns pixels at points in the same order.
func (s *state) GetMultiplePixels(
	ctx context.Context,
	points []image.Point,
) ([]color.RGBA, error) {
	ctx = contextutil.Default(ctx)
	if err := s.checkAvailable("pixel"); err != nil {
		return nil, err
	}
	s.traceAction("get-multiple-pixels count=%d", len(points))
	out := make([]color.RGBA, len(points))
	if len(points) == 0 {
		return out, nil
	}

	bounds, err := pointsBounds(points)
	if err != nil {
		return nil, s.gate.OperationError("pixel", err)
	}
	img, err := s.grab(ctx, bounds)
	if err != nil {
		return nil, err
	}
	// grab has already rejected nil, typed-nil, and malformed capture frames.
	imgBounds := img.Bounds()
	imagePoints, err := translatePointsToImage(points, bounds, imgBounds)
	if err != nil {
		return nil, s.gate.OperationError("pixel", err)
	}
	for i, imagePoint := range imagePoints {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := s.gate.EnsureOpen(); err != nil {
			return nil, err
		}
		if !imagePoint.In(imgBounds) {
			return nil, s.gate.OperationError(
				"pixel",
				fmt.Errorf("screen: capture bounds %v do not contain requested point %v", imgBounds, points[i]),
			)
		}
		rgba, ok := color.RGBAModel.Convert(
			img.At(imagePoint.X, imagePoint.Y),
		).(color.RGBA)
		if !ok {
			return nil, s.gate.OperationError("pixel", errors.New("screen: RGBA conversion returned unexpected type"))
		}
		out[i] = rgba
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.gate.EnsureOpen(); err != nil {
		return nil, err
	}
	return out, nil
}

// WaitForFn waits for fn to accept a captured image.
func (s *state) WaitForFn(
	ctx context.Context,
	rect image.Rectangle,
	fn func(context.Context, image.Image) bool,
	poll time.Duration,
) (image.Image, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return nil, err
	}
	s.traceAction("wait-for-fn rect=%s poll=%s", rect, poll)
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	img, err := find.WaitForFn(ctx, s.backend, rect, fn, poll)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, errors.Join(ctxErr, err)
	}
	if openErr := s.gate.EnsureOpen(); openErr != nil {
		return nil, errors.Join(openErr, err)
	}
	if err != nil {
		return nil, s.gate.OperationError("wait", err)
	}
	return img, nil
}

// WaitForSettle runs action and waits for the region to stabilize.
func (s *state) WaitForSettle(
	ctx context.Context,
	rect image.Rectangle,
	action func() error,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	s.traceAction(
		"wait-for-settle rect=%s stable=%d poll=%s",
		rect,
		stable,
		poll,
	)
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	before, err := s.grabHash(ctx, rect)
	if err != nil {
		return 0, err
	}
	if action != nil {
		if actionErr := action(); actionErr != nil {
			return 0, actionErr
		}
	}
	// An action may close its owning Session without canceling the caller's
	// context. Do not continue polling a backend after that ownership ends.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if openErr := s.gate.EnsureOpen(); openErr != nil {
		return 0, openErr
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
		return 0, s.gate.OperationError("wait-change", err)
	}
	// Change detection may close the Session while returning a valid hash.
	// Never start a new polling phase after its backend authority is revoked.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if openErr := s.gate.EnsureOpen(); openErr != nil {
		return 0, openErr
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
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, errors.Join(ctxErr, err)
	}
	if openErr := s.gate.EnsureOpen(); openErr != nil {
		return 0, errors.Join(openErr, err)
	}
	return stableHash, s.gate.OperationError("wait-stable", err)
}

// pointsBounds returns the smallest region containing every point, which is the
// region that has to be captured to read them all from one image.
func pointsBounds(points []image.Point) (image.Rectangle, error) {
	for _, point := range points {
		if point.X == math.MaxInt || point.Y == math.MaxInt {
			return image.Rectangle{}, fmt.Errorf(
				"screen: pixel coordinate overflows capture bounds: %v", point,
			)
		}
	}
	minX, minY := points[0].X, points[0].Y
	maxX, maxY := minX, minY
	for _, point := range points[1:] {
		minX = min(minX, point.X)
		minY = min(minY, point.Y)
		maxX = max(maxX, point.X)
		maxY = max(maxY, point.Y)
	}
	// The points are pixels to read, so the region has to include the last one
	// even though a rectangle's maximum is exclusive.
	return image.Rect(minX, minY, maxX+1, maxY+1), nil
}

// translatePointsToImage maps points given in desktop coordinates onto the
// pixels of a capture of the region they span.
//
// A capture of a desktop region is returned at the output's pixel scale, so the
// mapping is a ratio along each axis rather than a constant offset: with a
// doubled scale the point half way across the region is half way across the
// image, not at the same pixel offset. This is the inverse of Capture.ScreenPoint
// and uses the same rounding, so the two directions agree.
// A capture smaller than the region is refused, and Capture.ScreenPoint refuses the same
// one. Origin is not compared: the Capture contract allows image coordinates to have a
// different origin, so only the spans decide whether the image covers the region.
func translatePointsToImage(
	points []image.Point,
	region image.Rectangle,
	imageBounds image.Rectangle,
) ([]image.Point, error) {
	regionWidth, regionWidthOK := capture.AxisSpan(region.Min.X, region.Max.X)
	regionHeight, regionHeightOK := capture.AxisSpan(region.Min.Y, region.Max.Y)
	imageWidth, imageWidthOK := capture.AxisSpan(imageBounds.Min.X, imageBounds.Max.X)
	imageHeight, imageHeightOK := capture.AxisSpan(imageBounds.Min.Y, imageBounds.Max.Y)
	if !regionWidthOK || !regionHeightOK || !imageWidthOK || !imageHeightOK {
		return nil, errors.New("screen: capture region or image has empty bounds")
	}

	// A capture that came back smaller than the requested region is not a scaled
	// version of the whole region: it is the part the backend could actually get.
	// Scaling the region into it anyway maps a point near the clipped edge onto a
	// pixel that stands for a different place on screen. Require the two axes to
	// describe one uniform scale before translating anything, so a clipped capture
	// is refused rather than sampled from the wrong location.
	// Compared through 128-bit products. A capture span and a region span are each
	// derived from int coordinates, so on a wide enough synthetic rectangle the
	// 64-bit products wrap and two spans of different aspect ratios compare equal,
	// which would let a clipped capture through as a scaled one.
	lhsHi, lhsLo := bits.Mul64(imageWidth, regionHeight)
	rhsHi, rhsLo := bits.Mul64(imageHeight, regionWidth)
	if lhsHi != rhsHi || lhsLo != rhsLo {
		return nil, fmt.Errorf(
			"screen: capture %v is not a uniform scaling of region %v: the capture is clipped",
			imageBounds, region,
		)
	}
	// A capture carries at least one pixel per pixel of the region it covers, because
	// output scaling multiplies the region's pixels and never divides them. A ratio
	// below one is therefore not a smaller scale: it is a capture that came back with
	// part of the region missing, and reading a pixel out of it yields an averaged
	// value standing for somewhere else on screen.
	//
	// Clipping proportionally hides this. A region 100x50 whose right and bottom edges
	// are both cut by the same proportion comes back 50x25, which keeps the aspect
	// ratio and so passes a uniform-scale check, while every point past the midpoint
	// reads a different place on screen than the one asked for.
	if imageWidth < regionWidth || imageHeight < regionHeight {
		return nil, fmt.Errorf(
			"screen: capture %v has fewer pixels than region %v: it cannot be told apart from a clipped capture",
			imageBounds, region,
		)
	}

	out := make([]image.Point, 0, len(points))
	for _, point := range points {
		if !point.In(region) {
			return nil, fmt.Errorf(
				"screen: point %v is outside the captured region %v",
				point, region,
			)
		}
		out = append(out, image.Point{
			X: imageBounds.Min.X + int(capture.ScaleOffset(uint64(point.X-region.Min.X), regionWidth, imageWidth)),
			Y: imageBounds.Min.Y + int(capture.ScaleOffset(uint64(point.Y-region.Min.Y), regionHeight, imageHeight)),
		})
	}
	return out, nil
}

// WaitForNoChange waits until rect remains unchanged for stable samples.
func (s *state) WaitForNoChange(
	ctx context.Context,
	rect image.Rectangle,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	hash, err := find.WaitForNoChange(
		ctx,
		s.backend,
		rect,
		stable,
		poll,
		nil,
	)
	return hash, s.gate.OperationError("wait-stable", err)
}

// WaitForNoChangeFrom waits for rect to remain unchanged from initial.
func (s *state) WaitForNoChangeFrom(
	ctx context.Context,
	rect image.Rectangle,
	initial uint32,
	stable int,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-stable"); err != nil {
		return 0, err
	}
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	hash, err := find.WaitForNoChangeFrom(
		ctx,
		s.backend,
		rect,
		initial,
		stable,
		poll,
		nil,
	)
	return hash, s.gate.OperationError("wait-stable", err)
}

// FindColor returns the first pixel in rect within tolerance of target.
func (s *state) FindColor(
	ctx context.Context,
	rect image.Rectangle,
	target color.RGBA,
	tolerance int,
) (image.Point, error) {
	if err := s.checkAvailable("pixel"); err != nil {
		return image.Point{}, err
	}
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	point, err := find.FindColor(ctx, s.backend, rect, target, tolerance)
	return point, s.gate.OperationError("pixel", err)
}

// WaitForChange waits until rect differs from initial.
func (s *state) WaitForChange(
	ctx context.Context,
	rect image.Rectangle,
	initial uint32,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait-change"); err != nil {
		return 0, err
	}
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	hash, err := find.WaitForChange(
		ctx,
		s.backend,
		rect,
		initial,
		poll,
		nil,
	)
	return hash, s.gate.OperationError("wait-change", err)
}

// WaitFor waits until rect has the requested hash.
func (s *state) WaitFor(
	ctx context.Context,
	rect image.Rectangle,
	want uint32,
	poll time.Duration,
) (uint32, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return 0, err
	}
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	hash, err := find.WaitFor(ctx, s.backend, rect, want, poll, nil)
	return hash, s.gate.OperationError("wait", err)
}

// ScanFor waits for any requested hash across the supplied rectangles.
func (s *state) ScanFor(
	ctx context.Context,
	rects []image.Rectangle,
	wants []uint32,
	poll time.Duration,
) (find.Result, error) {
	if err := s.checkAvailable("wait"); err != nil {
		return find.Result{}, err
	}
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	result, err := find.ScanFor(ctx, s.backend, rects, wants, poll, nil)
	return result, s.gate.OperationError("wait", err)
}

// Resolution returns the active capture resolution.
func (s *state) Resolution(ctx context.Context) (int, int, error) {
	if err := s.checkAvailable("resolution"); err != nil {
		return 0, 0, err
	}
	ctx, cancel := s.gate.BackendContext(ctx)
	defer cancel()
	width, height, err := screen.ResolutionWithContext(ctx, s.backend)
	return width, height, s.gate.OperationError("resolution", err)
}

// findScreenValue shares the session-bound lifetime and result authority checks
// of screen recognition operations. Callers retain their validation, capability
// admission, backend selection, and tracing order.
func findScreenValue[T any](ctx context.Context, s *state, operation string, recognize func(context.Context) (T, error)) (T, error) {
	var zero T
	operationCtx, timeoutCancel := s.gate.BackendContext(ctx)
	defer timeoutCancel()
	operationCtx, cancel := context.WithCancel(operationCtx)
	defer cancel()
	stopSessionCancel := s.gate.AfterClose(cancel) //nolint:contextcheck // Session closure independently revokes recognition.
	defer stopSessionCancel()
	result, err := recognize(operationCtx)
	if ctxErr := operationCtx.Err(); ctxErr != nil {
		return zero, errors.Join(ctxErr, s.gate.OperationError(operation, err))
	}
	if openErr := s.gate.EnsureOpen(); openErr != nil {
		return zero, errors.Join(openErr, s.gate.OperationError(operation, err))
	}
	if err != nil {
		return zero, s.gate.OperationError(operation, err)
	}
	return result, nil
}

// FindTemplate searches rect for the best match of reference whose similarity
// score is at least minConfidence. The returned rectangle is in desktop
// coordinates, including when the backend returns a uniformly upscaled image.
// It requires a backend that can attest complete capture coverage; unsupported
// or clipped live captures return find.ErrUnverifiedCaptureGeometry.
//
// minConfidence uses the normalized RGB mean-absolute-difference score defined
// by find.MatchTemplate. It is a similarity measure, not a probability.
// For exact pixel matching, use find.LocateExact.
func (s *state) FindTemplate(
	ctx context.Context,
	rect image.Rectangle,
	reference image.Image,
	minConfidence float64,
) (find.TemplateMatch, error) {
	if err := s.checkAvailable("capture"); err != nil {
		return find.TemplateMatch{}, err
	}
	s.traceAction("find-template rect=%s min-confidence=%.4f", rect, minConfidence)
	return findScreenValue(ctx, s, "find-template", func(operationCtx context.Context) (find.TemplateMatch, error) {
		return find.MatchTemplate(operationCtx, s.backend, rect, reference, minConfidence)
	})
}

// FindTemplates returns up to maxResults non-overlapping matches in descending
// similarity order. It uses one verified capture and the same cancellation and
// Session authority checks as FindTemplate. An empty slice means no matches.
func (s *state) FindTemplates(ctx context.Context, rect image.Rectangle, reference image.Image, minConfidence float64, maxResults int) ([]find.TemplateMatch, error) {
	if err := s.checkAvailable("capture"); err != nil {
		return nil, err
	}
	s.traceAction("find-templates rect=%s min-confidence=%.4f max=%d", rect, minConfidence, maxResults)
	return findScreenValue(ctx, s, "find-templates", func(operationCtx context.Context) ([]find.TemplateMatch, error) {
		return find.MatchTemplates(operationCtx, s.backend, rect, reference, minConfidence, maxResults)
	})
}

// FindText recognizes rendered text in one authorized, attested screen region.
// OCR is opt-in: callers must provide a local recognizer explicitly. An empty
// Matches result with nil error means recognition succeeded but the query did
// not occur. OCR confidence is provider-specific and is not a probability.
func (s *state) FindText(ctx context.Context, rect image.Rectangle, query string, options find.TextOptions) (find.TextResult, error) {
	if err := find.ValidateTextOptions(query, options); err != nil {
		return find.TextResult{}, err
	}
	if err := s.checkAvailable("capture"); err != nil {
		return find.TextResult{}, err
	}
	verified, ok := s.backend.(find.VerifiedTemplateScreenshotter)
	if !ok {
		return find.TextResult{}, fmt.Errorf("screen: OCR requires backend-verified capture geometry: %w", find.ErrOCRGeometry)
	}
	s.traceAction("find-text rect=%s", rect)
	return findScreenValue(ctx, s, "find-text", func(operationCtx context.Context) (find.TextResult, error) {
		return find.RecognizeText(operationCtx, verified, rect, query, options)
	})
}

// Capture captures rect and preserves the requested desktop region represented
// by the returned image. An empty rect is rejected because the screenshot
// backend does not expose a backend-independent desktop rectangle for an
// image-only full-screen capture.
func (s *state) Capture(ctx context.Context, rect image.Rectangle) (screen.Capture, error) {
	s.traceAction("capture rect=%s", rect)
	if rect.Empty() {
		return screen.Capture{}, fmt.Errorf("screen: capture provenance requires a non-empty rectangle: %w", capability.ErrInvalidArgument)
	}
	img, err := s.grab(ctx, rect)
	if err != nil {
		return screen.Capture{}, err
	}
	if util.IsNil(img) {
		return screen.Capture{}, s.gate.OperationError("capture", fmt.Errorf("screen: capture returned nil image"))
	}

	return screen.Capture{Image: img, ScreenRect: rect}, nil
}

// Admission is the screen-local authority and policy seam. A capture cannot
// access other backends or change session identity.
type Admission interface {
	CheckBackend(operation string, backend any) error
	BackendContext(context.Context) (context.Context, context.CancelFunc)
	Tracef(component, format string, args ...any)
	EnsureOpen() error
	OperationError(operation string, err error) error
	AfterClose(func()) func() bool
}

type Owner struct{ *state }
type Handle struct{ *state }

func New(gate Admission, backend screen.Screenshotter) *Owner {
	return &Owner{&state{gate: gate, backend: backend}}
}
func (o *Owner) Handle() Handle {
	if o == nil {
		return Handle{}
	}
	return Handle{o.state}
}
func (o *Owner) Install(backend screen.Screenshotter) { o.backend = backend }
func (o *Owner) Backend() screen.Screenshotter {
	if o == nil {
		return nil
	}
	return o.backend
}
func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	return o.close()
}
func (o *Owner) CheckAvailable(operation string) error {
	if o == nil {
		return capability.ErrNilSession
	}
	return o.checkAvailable(operation)
}
func (o *Owner) SavePNG(ctx context.Context, img image.Image, path string) error {
	return o.savePNG(ctx, img, path)
}
