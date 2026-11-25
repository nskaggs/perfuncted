package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"
)

type templateStaticScreen struct {
	frame image.Image
	err   error
	calls int
	seen  image.Rectangle
}

func (s *templateStaticScreen) Grab(_ context.Context, rect image.Rectangle) (image.Image, error) {
	s.calls++
	s.seen = rect
	return s.frame, s.err
}

func (s *templateStaticScreen) GrabVerified(ctx context.Context, rect image.Rectangle) (VerifiedTemplateCapture, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return VerifiedTemplateCapture{}, err
	}
	return VerifiedTemplateCapture{Image: img, DesktopRect: rect, PixelScale: 1}, nil
}

func templatePattern(bounds image.Rectangle) *image.NRGBA {
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			px := x - bounds.Min.X
			py := y - bounds.Min.Y
			ref.SetNRGBA(x, y, color.NRGBA{R: uint8(80 + px*23 + py*9), G: uint8(10 + px*7 + py*31), B: uint8(170 - px*11 - py*17), A: 255})
		}
	}
	return ref
}

func fillTemplateFrame(bounds image.Rectangle) *image.RGBA {
	frame := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			frame.SetRGBA(x, y, color.RGBA{R: 3, G: 7, B: 13, A: 255})
		}
	}
	return frame
}

func paintTemplate(frame *image.RGBA, ref image.Image, x, y int) {
	bounds := ref.Bounds()
	for dy := 0; dy < bounds.Dy(); dy++ {
		for dx := 0; dx < bounds.Dx(); dx++ {
			c := color.RGBAModel.Convert(ref.At(bounds.Min.X+dx, bounds.Min.Y+dy)).(color.RGBA) //nolint:errcheck // RGBAModel.Convert always returns a color.RGBA
			frame.SetRGBA(x+dx, y+dy, c)
		}
	}
}

func TestMatchTemplatePositionIndependentFuzzyAndSingleCapture(t *testing.T) {
	ref := templatePattern(image.Rect(11, 13, 16, 17))
	frame := fillTemplateFrame(image.Rect(0, 0, 80, 50))
	paintTemplate(frame, ref, 47, 29)
	frame.SetRGBA(49, 30, color.RGBA{R: 112, G: 55, B: 127, A: 255})
	screen := &templateStaticScreen{frame: frame}
	area := image.Rect(100, 200, 180, 250)
	result, err := MatchTemplate(context.Background(), screen, area, ref, 0.90)
	if err != nil {
		t.Fatal(err)
	}
	if want := image.Rect(147, 229, 152, 233); result.Rect != want {
		t.Fatalf("matched rect = %v, want %v", result.Rect, want)
	}
	if result.Confidence <= 0.90 || result.Confidence >= 1 {
		t.Fatalf("fuzzy confidence = %v, want (0.90,1)", result.Confidence)
	}
	if screen.calls != 1 || screen.seen != area {
		t.Fatalf("expected one scoped capture, calls=%d rect=%v", screen.calls, screen.seen)
	}
	if _, err := MatchTemplate(context.Background(), screen, area, ref, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("confidence 1 must reject perturbed image, got %v", err)
	}
}

func TestMatchTemplateTransparentPixelsMaskedAndAllTransparentRejected(t *testing.T) {
	ref := templatePattern(image.Rect(5, 7, 8, 10))
	ref.SetNRGBA(5, 7, color.NRGBA{R: 240, G: 90, B: 70, A: 0})
	frame := fillTemplateFrame(image.Rect(0, 0, 15, 13))
	paintTemplate(frame, ref, 8, 6)
	// Fully transparent template pixels do not constrain the screenshot.
	frame.SetRGBA(8, 6, color.RGBA{R: 251, G: 244, B: 233, A: 255})
	result, err := MatchTemplateInImage(context.Background(), frame, frame.Bounds(), ref, 1)
	if err != nil || result.Rect != image.Rect(8, 6, 11, 9) || result.Confidence != 1 {
		t.Fatalf("transparent mask result = %+v, err = %v", result, err)
	}
	for i := range ref.Pix {
		ref.Pix[i] = 0
	}
	if got, err := MatchTemplateInImage(context.Background(), frame, frame.Bounds(), ref, 0); !errors.Is(err, ErrEmptyRegion) || got != (TemplateMatch{}) {
		t.Fatalf("empty mask result = %+v, err = %v", got, err)
	}
}

func TestMatchTemplateNrgbaAlphaAndGenericSubimage(t *testing.T) {
	ref := templatePattern(image.Rect(9, 12, 12, 14))
	frame := fillTemplateFrame(image.Rect(10, 15, 30, 35))
	paintTemplate(frame, ref, 14, 22)
	crop := frame.SubImage(image.Rect(12, 17, 28, 33))
	got, err := MatchTemplateInImage(context.Background(), crop, image.Rect(200, 300, 216, 316), ref, 1)
	if err != nil || got.Rect != image.Rect(202, 305, 205, 307) || got.Confidence != 1 {
		t.Fatalf("nonzero origin + subimage = %+v, %v", got, err)
	}
}

func TestMatchTemplateHiDPIMappingAndUnsafeCaptureRejection(t *testing.T) {
	ref := templatePattern(image.Rect(0, 0, 4, 4))
	frame := fillTemplateFrame(image.Rect(0, 0, 20, 20))
	paintTemplate(frame, ref, 6, 10)
	got, err := MatchTemplateInImage(context.Background(), frame, image.Rect(100, 200, 110, 210), ref, 1)
	if err != nil || got.Rect != image.Rect(103, 205, 105, 207) {
		t.Fatalf("2x HiDPI result = %+v, err=%v", got, err)
	}
	for _, tc := range []struct {
		name string
		img  image.Image
		area image.Rectangle
	}{
		{"downscale or clip", fillTemplateFrame(image.Rect(0, 0, 6, 6)), image.Rect(0, 0, 10, 10)},
		{"nonuniform scale", fillTemplateFrame(image.Rect(0, 0, 20, 18)), image.Rect(0, 0, 10, 10)},
		{"empty screenshot", image.NewRGBA(image.Rectangle{}), image.Rect(0, 0, 10, 10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := MatchTemplateInImage(context.Background(), tc.img, tc.area, ref, 0.95); !errors.Is(err, ErrInvalidFrame) || got != (TemplateMatch{}) {
				t.Fatalf("unsafe geometry result=%+v, err=%v", got, err)
			}
		})
	}
}

func TestMatchTemplateErrorsFailClosed(t *testing.T) {
	ref := templatePattern(image.Rect(0, 0, 2, 2))
	frame := fillTemplateFrame(image.Rect(0, 0, 8, 8))
	area := frame.Bounds()
	for _, threshold := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got, err := MatchTemplateInImage(context.Background(), frame, area, ref, threshold); err == nil || got != (TemplateMatch{}) {
			t.Fatalf("threshold %v: %+v, %v", threshold, got, err)
		}
	}
	if got, err := MatchTemplateInImage(context.Background(), frame, area, ref, 1); !errors.Is(err, ErrNotFound) || got != (TemplateMatch{}) {
		t.Fatalf("no match result=%+v, err=%v", got, err)
	}
	tooLarge := templatePattern(image.Rect(0, 0, 9, 9))
	if got, err := MatchTemplateInImage(context.Background(), frame, area, tooLarge, 0.95); !errors.Is(err, ErrNotFound) || got != (TemplateMatch{}) {
		t.Fatalf("oversized reference result=%+v, err=%v", got, err)
	}
	malformed := fillTemplateFrame(image.Rect(0, 0, 8, 8))
	malformed.Pix = malformed.Pix[:2]
	if got, err := MatchTemplateInImage(context.Background(), malformed, area, ref, 0.95); !errors.Is(err, ErrInvalidFrame) || got != (TemplateMatch{}) {
		t.Fatalf("malformed image result=%+v, err=%v", got, err)
	}
	if got, err := MatchTemplateInImage(context.Background(), frame, image.Rectangle{}, ref, 0.95); !errors.Is(err, ErrEmptyRegion) || got != (TemplateMatch{}) {
		t.Fatalf("empty search area result=%+v, err=%v", got, err)
	}
	if got, err := MatchTemplateInImage(context.Background(), frame, area, nil, 0.95); err == nil || got != (TemplateMatch{}) {
		t.Fatalf("nil reference result=%+v, err=%v", got, err)
	}
	var typedNil *image.RGBA
	if got, err := MatchTemplateInImage(context.Background(), typedNil, area, ref, 0.95); err == nil || got != (TemplateMatch{}) {
		t.Fatalf("typed-nil screenshot result=%+v, err=%v", got, err)
	}
	failure := errors.New("synthetic backend")
	if got, err := MatchTemplate(context.Background(), &templateStaticScreen{err: failure}, area, ref, 0.95); !errors.Is(err, failure) || got != (TemplateMatch{}) {
		t.Fatalf("backend failure result=%+v, err=%v", got, err)
	}
	if got, err := MatchTemplate(context.Background(), nil, area, ref, 0.95); err == nil || got != (TemplateMatch{}) {
		t.Fatalf("missing backend result=%+v, err=%v", got, err)
	}
}

func TestMatchTemplateDeterministicTieAndCancellation(t *testing.T) {
	ref := templatePattern(image.Rect(0, 0, 3, 3))
	frame := fillTemplateFrame(image.Rect(0, 0, 30, 30))
	paintTemplate(frame, ref, 10, 2)
	paintTemplate(frame, ref, 2, 10)
	got, err := MatchTemplateInImage(context.Background(), frame, frame.Bounds(), ref, 1)
	if err != nil || got.Rect != image.Rect(10, 2, 13, 5) {
		t.Fatalf("first exact match tie=%+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := MatchTemplateInImage(ctx, frame, frame.Bounds(), ref, 0.9); !errors.Is(err, context.Canceled) || got != (TemplateMatch{}) {
		t.Fatalf("pre-canceled result=%+v, err=%v", got, err)
	}
}

// A context that cancels after a deterministic number of checks proves the
// CPU-bound search checks authority during iteration, not only around Grab.
type templateCheckCancelContext struct {
	context.Context
	checks int
	after  int
}

func (c *templateCheckCancelContext) Err() error {
	c.checks++
	if c.checks >= c.after {
		return context.Canceled
	}
	return nil
}

func TestMatchTemplateCancellationMidSearchDropsPartialResults(t *testing.T) {
	ref := templatePattern(image.Rect(0, 0, 2, 2))
	frame := fillTemplateFrame(image.Rect(0, 0, 120, 120))
	ctx := &templateCheckCancelContext{Context: context.Background(), after: 20}
	got, err := MatchTemplateInImage(ctx, frame, frame.Bounds(), ref, 0.99)
	if !errors.Is(err, context.Canceled) || got != (TemplateMatch{}) || ctx.checks < 20 {
		t.Fatalf("mid-search cancellation result=%+v, err=%v, checks=%d", got, err, ctx.checks)
	}
}

func BenchmarkMatchTemplateRGBA(b *testing.B) {
	ref := templatePattern(image.Rect(0, 0, 16, 16))
	frame := fillTemplateFrame(image.Rect(0, 0, 256, 256))
	paintTemplate(frame, ref, 188, 143)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := MatchTemplateInImage(context.Background(), frame, frame.Bounds(), ref, 0.95); err != nil {
			b.Fatal(err)
		}
	}
}

type cancelDuringTemplateDraw struct {
	bounds image.Rectangle
	cancel context.CancelFunc
	calls  int
}

func (img *cancelDuringTemplateDraw) ColorModel() color.Model { return color.RGBAModel }
func (img *cancelDuringTemplateDraw) Bounds() image.Rectangle { return img.bounds }
func (img *cancelDuringTemplateDraw) At(_, _ int) color.Color {
	img.calls++
	if img.calls == 10 {
		img.cancel()
	}
	return color.RGBA{R: 30, G: 40, B: 50, A: 255}
}

func TestMatchTemplateCancellationDuringImageConversion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &cancelDuringTemplateDraw{bounds: image.Rect(0, 0, 128, 128), cancel: cancel}
	ref := templatePattern(image.Rect(0, 0, 2, 2))
	got, err := MatchTemplateInImage(ctx, src, src.bounds, ref, 0.95)
	if !errors.Is(err, context.Canceled) || got != (TemplateMatch{}) || src.calls < 10 {
		t.Fatalf("image conversion cancellation = %+v, %v, sampled=%d", got, err, src.calls)
	}
}

func TestMatchTemplateZeroConfidenceAcceptsMaximalDifference(t *testing.T) {
	src := image.NewRGBA(image.Rect(25, 31, 26, 32))
	src.SetRGBA(25, 31, color.RGBA{A: 255})
	ref := image.NewNRGBA(image.Rect(3, 7, 4, 8))
	ref.SetNRGBA(3, 7, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	area := image.Rect(-50, 100, -49, 101)
	match, err := MatchTemplateInImage(context.Background(), src, area, ref, 0)
	if err != nil || match.Rect != area || match.Confidence != 0 {
		t.Fatalf("zero threshold must accept maximal difference: result=%+v, error=%v", match, err)
	}
	if got, err := MatchTemplateInImage(context.Background(), src, area, ref, 0.000001); !errors.Is(err, ErrNotFound) || got != (TemplateMatch{}) {
		t.Fatalf("positive threshold must reject zero confidence: result=%+v, error=%v", got, err)
	}
}

func BenchmarkMatchTemplateFullHD(b *testing.B) {
	const (
		width          = 1920
		height         = 1080
		templateWidth  = 16
		templateHeight = 16
	)
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value := uint32(x*73856093 ^ y*19349663)
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255})
		}
	}
	reference := image.NewNRGBA(image.Rect(0, 0, templateWidth, templateHeight))
	for y := 0; y < templateHeight; y++ {
		for x := 0; x < templateWidth; x++ {
			value := uint32((x+17)*83492791 ^ (y+11)*2971215073)
			reference.SetNRGBA(x, y, color.NRGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255})
		}
	}
	b.ReportAllocs()
	b.SetBytes(int64(width * height * 4))
	b.ResetTimer()
	for b.Loop() {
		_, err := MatchTemplateInImage(context.Background(), source, source.Bounds(), reference, 0.95)
		if !errors.Is(err, ErrNotFound) {
			b.Fatalf("MatchTemplateInImage error = %v, want ErrNotFound", err)
		}
	}
}

type genericTemplateImage struct{ image.Image }

func (g genericTemplateImage) ColorModel() color.Model { return g.Image.ColorModel() }
func (g genericTemplateImage) Bounds() image.Rectangle { return g.Image.Bounds() }
func (g genericTemplateImage) At(x, y int) color.Color { return g.Image.At(x, y) }

func TestMatchTemplateGenericImagesAndNonzeroSubimageCoordinates(t *testing.T) {
	referencePixels := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	referencePixels.SetNRGBA(0, 0, color.NRGBA{R: 20, G: 40, B: 60, A: 255})
	referencePixels.SetNRGBA(1, 0, color.NRGBA{R: 80, G: 100, B: 120, A: 255})
	referencePixels.SetNRGBA(0, 1, color.NRGBA{R: 140, G: 160, B: 180, A: 255})
	referencePixels.SetNRGBA(1, 1, color.NRGBA{R: 200, G: 220, B: 240, A: 255})
	sourcePixels := image.NewNRGBA(image.Rect(10, 20, 16, 24))
	for y := sourcePixels.Bounds().Min.Y; y < sourcePixels.Bounds().Max.Y; y++ {
		for x := sourcePixels.Bounds().Min.X; x < sourcePixels.Bounds().Max.X; x++ {
			sourcePixels.SetNRGBA(x, y, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
		}
	}
	for dy := 0; dy < 2; dy++ {
		for dx := 0; dx < 2; dx++ {
			sourcePixels.Set(12+dx, 21+dy, referencePixels.At(dx, dy))
		}
	}
	// A wrapper keeps both images on the generic image.Image path instead of
	// allowing packed RGBA/NRGBA special cases to satisfy this test.
	source := genericTemplateImage{Image: sourcePixels}
	reference := genericTemplateImage{Image: referencePixels}
	area := image.Rect(-30, 40, -24, 44)
	got, err := MatchTemplateInImage(context.Background(), source, area, reference, 1)
	if err != nil {
		t.Fatalf("MatchTemplateInImage generic images: %v", err)
	}
	if want := image.Rect(-28, 41, -26, 43); got.Rect != want || got.Confidence != 1 {
		t.Fatalf("generic nonzero-origin result = %+v, want %v at 1", got, want)
	}
}

func TestMatchTemplateSemiTransparentReferenceWeightsPixelError(t *testing.T) {
	reference := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	reference.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	reference.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 128})
	source := image.NewRGBA(image.Rect(0, 0, 2, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	source.SetRGBA(1, 0, color.RGBA{G: 205, A: 255})
	got, err := MatchTemplateInImage(context.Background(), source, source.Bounds(), reference, 0.97)
	if err != nil {
		t.Fatalf("semi-transparent weighted match: %v", err)
	}
	want := 1 - float64(128*50)/float64((255+128)*3*255)
	if got.Rect != source.Bounds() || math.Abs(got.Confidence-want) > 1e-12 {
		t.Fatalf("semi-transparent score = %+v, want confidence %.12f", got, want)
	}
}

type boundsOnlyTemplateImage struct{ bounds image.Rectangle }

func (g boundsOnlyTemplateImage) ColorModel() color.Model { return color.RGBAModel }
func (g boundsOnlyTemplateImage) Bounds() image.Rectangle { return g.bounds }
func (boundsOnlyTemplateImage) At(int, int) color.Color {
	panic("bounds-only image pixels must not be read")
}

func TestMatchTemplateRejectsOverflowingGeometryBeforeReadingPixels(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	reference := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	for _, bounds := range []image.Rectangle{
		image.Rect(0, 0, maxInt, 2),
		image.Rect(minInt, 0, maxInt, 1),
	} {
		t.Run(bounds.String(), func(t *testing.T) {
			source := boundsOnlyTemplateImage{bounds: bounds}
			got, err := MatchTemplateInImage(context.Background(), source, bounds, reference, 0.95)
			if !errors.Is(err, ErrInvalidFrame) || got != (TemplateMatch{}) {
				t.Fatalf("overflowing bounds returned %+v, %v; want invalid frame", got, err)
			}
		})
	}
}

// A 2x desktop clips one logical pixel on both sides: the resulting pixel
// ratio is 1.8, but does not describe a valid capture of the requested area.
func TestMatchTemplateRejectsAmbiguousProportionallyClippedHiDPICapture(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 18, 18))
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	ref.SetRGBA(0, 0, color.RGBA{R: 253, G: 62, B: 91, A: 255})
	frame.SetRGBA(0, 0, color.RGBA{R: 253, G: 62, B: 91, A: 255})
	area := image.Rect(-1, -1, 9, 9)
	got, err := MatchTemplateInImage(context.Background(), frame, area, ref, 1)
	if err == nil || got != (TemplateMatch{}) {
		t.Fatalf("clipped capture returned unverifiable desktop coordinates %+v, err=%v", got, err)
	}
}

func TestMatchTemplateThresholdEqualityMustNotBePrunedByRounding(t *testing.T) {
	reference := image.NewRGBA(image.Rect(0, 0, 1, 1))
	reference.SetRGBA(0, 0, color.RGBA{R: 210, G: 180, B: 130, A: 255})
	source := image.NewRGBA(image.Rect(0, 0, 1, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 209, G: 180, B: 130, A: 255})
	confidence := 1 - float64(255)/float64(255*3*255)
	if math.IsNaN(confidence) || confidence <= 0 || confidence >= 1 {
		t.Fatal("invalid test confidence")
	}
	got, err := MatchTemplateInImage(context.Background(), source, source.Bounds(), reference, confidence)
	if err != nil || got.Rect != source.Bounds() || got.Confidence != confidence {
		t.Fatalf("score-equality candidate was incorrectly pruned: match=%+v, err=%v, threshold=%.17g", got, err, confidence)
	}
}

type wideCancelTemplateImage struct {
	ctxBounds image.Rectangle
	cancel    context.CancelFunc
	calls     int
}

func (img *wideCancelTemplateImage) ColorModel() color.Model { return color.RGBAModel }
func (img *wideCancelTemplateImage) Bounds() image.Rectangle { return img.ctxBounds }
func (img *wideCancelTemplateImage) At(x, y int) color.Color {
	img.calls++
	if img.calls == 10 {
		img.cancel()
	}
	return color.RGBA{R: 15, G: 20, B: 25, A: 255}
}

func TestMatchTemplateCancellationInspectsAtMostBoundedPixelsInSingleWideRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &wideCancelTemplateImage{ctxBounds: image.Rect(0, 0, 100000, 1), cancel: cancel}
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	ref.SetRGBA(0, 0, color.RGBA{R: 15, G: 20, B: 25, A: 255})
	got, err := MatchTemplateInImage(ctx, source, source.Bounds(), ref, 0.95)
	if !errors.Is(err, context.Canceled) || got != (TemplateMatch{}) {
		t.Fatalf("wide-row cancellation returned %+v, err=%v", got, err)
	}
	if source.calls > 5000 {
		t.Fatalf("read %d pixels after cancellation began; want at most 5000", source.calls)
	}
}

type templateBoundsWithoutPixels struct{ bounds image.Rectangle }

func (img templateBoundsWithoutPixels) ColorModel() color.Model { return color.RGBAModel }
func (img templateBoundsWithoutPixels) Bounds() image.Rectangle { return img.bounds }
func (templateBoundsWithoutPixels) At(int, int) color.Color {
	panic("pixel access must not happen for an oversized image")
}

func TestMatchTemplateRejectsOversizedCaptureAndReferenceBeforeReading(t *testing.T) {
	onePixel := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, tc := range []struct {
		name      string
		source    image.Image
		reference image.Image
		area      image.Rectangle
	}{
		{
			name:      "capture over 16 Mi pixels",
			source:    templateBoundsWithoutPixels{image.Rect(0, 0, 4097, 4096)},
			reference: onePixel,
			area:      image.Rect(0, 0, 4097, 4096),
		},
		{
			name:      "reference over 2 Mi pixels",
			source:    templateBoundsWithoutPixels{image.Rect(0, 0, 2049, 1024)},
			reference: templateBoundsWithoutPixels{image.Rect(0, 0, 2049, 1024)},
			area:      image.Rect(0, 0, 2049, 1024),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchTemplateInImage(context.Background(), tc.source, tc.area, tc.reference, 0.95)
			if !errors.Is(err, ErrInvalidFrame) || got != (TemplateMatch{}) {
				t.Fatalf("oversized image unexpectedly matched: %+v, err=%v", got, err)
			}
		})
	}
}

type templateVerifiedFixture struct {
	capture VerifiedTemplateCapture
	err     error
	calls   int
}

func (s *templateVerifiedFixture) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return s.capture.Image, s.err
}

func (s *templateVerifiedFixture) GrabVerified(context.Context, image.Rectangle) (VerifiedTemplateCapture, error) {
	s.calls++
	return s.capture, s.err
}

type templateUnverifiedFixture struct {
	frame image.Image
	calls int
}

func (s *templateUnverifiedFixture) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	return s.frame, nil
}

func TestMatchTemplateRequiresTrustedCaptureAndRejectsHiddenScaleClipping(t *testing.T) {
	area := image.Rect(-1, -1, 9, 9)
	reference := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	reference.SetNRGBA(0, 0, color.NRGBA{R: 250, G: 20, B: 30, A: 255})

	t.Run("legacy screenshotter fails closed before capture", func(t *testing.T) {
		backend := &templateUnverifiedFixture{frame: image.NewRGBA(image.Rect(0, 0, 20, 20))}
		got, err := MatchTemplate(context.Background(), backend, area, reference, 1)
		if !errors.Is(err, ErrUnverifiedCaptureGeometry) || got != (TemplateMatch{}) || backend.calls != 0 {
			t.Fatalf("unverified live capture = %+v, %v, calls=%d", got, err, backend.calls)
		}
	})

	t.Run("integer 2x crop cannot masquerade as complete capture", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 18, 18))
		frame.SetRGBA(0, 0, color.RGBA{R: 250, G: 20, B: 30, A: 255})
		backend := &templateVerifiedFixture{capture: VerifiedTemplateCapture{
			Image: frame, DesktopRect: area, PixelScale: 2,
		}}
		got, err := MatchTemplate(context.Background(), backend, area, reference, 1)
		if !errors.Is(err, ErrUnverifiedCaptureGeometry) || got != (TemplateMatch{}) {
			t.Fatalf("clipped verified capture = %+v, %v", got, err)
		}
	})

	t.Run("complete 2x capture maps to desktop coordinates", func(t *testing.T) {
		frame := image.NewRGBA(image.Rect(0, 0, 20, 20))
		frame.SetRGBA(8, 4, color.RGBA{R: 250, G: 20, B: 30, A: 255})
		backend := &templateVerifiedFixture{capture: VerifiedTemplateCapture{
			Image: frame, DesktopRect: area, PixelScale: 2,
		}}
		got, err := MatchTemplate(context.Background(), backend, area, reference, 1)
		if err != nil || got.Rect != image.Rect(3, 1, 4, 2) || got.Confidence != 1 {
			t.Fatalf("verified 2x capture = %+v, %v", got, err)
		}
	})
}

func TestMatchTemplateRejectsIncorrectVerifiedDesktopOrigin(t *testing.T) {
	area := image.Rect(10, 20, 14, 24)
	backend := &templateVerifiedFixture{capture: VerifiedTemplateCapture{
		Image: image.NewRGBA(image.Rect(0, 0, 4, 4)), DesktopRect: image.Rect(9, 20, 13, 24), PixelScale: 1,
	}}
	got, err := MatchTemplate(context.Background(), backend, area, image.NewRGBA(image.Rect(0, 0, 1, 1)), 0)
	if !errors.Is(err, ErrUnverifiedCaptureGeometry) || got != (TemplateMatch{}) {
		t.Fatalf("wrong-origin capture = %+v, %v", got, err)
	}
}
