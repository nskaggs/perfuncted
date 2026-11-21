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
