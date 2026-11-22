package find

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/util"
	"image"
	"image/color"
	"testing"
)

func TestMatchTemplatesStableOrderSuppressionAndBestParity(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 7, 3))
	ref := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	ref.SetNRGBA(1, 0, color.NRGBA{R: 255, A: 255})
	for _, x := range []int{0, 1, 4} {
		src.SetNRGBA(x, 0, color.NRGBA{R: 255, A: 255})
		src.SetNRGBA(x+1, 0, color.NRGBA{R: 255, A: 255})
	}
	// The candidate at x=1 overlaps x=0; the two exact distinct copies survive.
	matches, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, 1, 64)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].Rect.Min.X != 0 || matches[1].Rect.Min.X != 4 {
		t.Fatalf("matches=%v", matches)
	}
	best, err := MatchTemplateInImage(context.Background(), src, src.Bounds(), ref, 1)
	if err != nil || best != matches[0] {
		t.Fatalf("best=%v err=%v all=%v", best, err, matches)
	}
	limited, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, 1, 1)
	if err != nil || len(limited) != 1 || limited[0] != best {
		t.Fatalf("limited=%v err=%v", limited, err)
	}
}

func TestMatchTemplatesEmptyInvalidAndFuzzyRanking(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 200, A: 255})
	src.SetNRGBA(2, 0, color.NRGBA{R: 255, A: 255})
	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	matches, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, .9, 64)
	if err != nil || len(matches) != 2 || matches[0].Rect.Min.X != 2 || matches[1].Rect.Min.X != 0 || matches[0].Confidence != 1 {
		t.Fatalf("rank=%v err=%v", matches, err)
	}
	empty, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, 1, 64)
	if err != nil || len(empty) != 1 {
		t.Fatalf("exact=%v err=%v", empty, err)
	}
	other := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	other.SetNRGBA(0, 0, color.NRGBA{B: 255, A: 255})
	no, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), other, 1, 64)
	if err != nil || len(no) != 0 {
		t.Fatalf("no match=%v %v", no, err)
	}
	for _, limit := range []int{-1, 0, 65} {
		_, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, 1, limit)
		if !errors.Is(err, ErrInvalidFrame) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	blank := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	_, err = MatchTemplatesInImage(context.Background(), src, src.Bounds(), blank, 1, 64)
	if !errors.Is(err, ErrEmptyRegion) {
		t.Fatalf("transparent: %v", err)
	}
	no, err = MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, .0, 64)
	if err != nil || len(no) != 3 {
		t.Fatalf("zero threshold=%v %v", no, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	no, err = MatchTemplatesInImage(ctx, src, src.Bounds(), ref, 0, 64)
	if !errors.Is(err, context.Canceled) || no != nil {
		t.Fatalf("canceled: %v %v", no, err)
	}
}

func TestMatchTemplatesVerifiedCaptureAndErrors(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	area := image.Rect(-4, 8, 0, 10)
	src := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	for _, pt := range []image.Point{{2, 0}, {6, 2}} {
		src.SetNRGBA(pt.X, pt.Y, color.NRGBA{R: 255, A: 255})
	}
	fixture := &templateVerifiedFixture{capture: VerifiedTemplateCapture{Image: src, DesktopRect: area, PixelScale: 2}}
	matches, err := MatchTemplates(context.Background(), fixture, area, ref, 1, 64)
	if err != nil || len(matches) != 2 || matches[0].Rect != image.Rect(-3, 8, -2, 9) || matches[1].Rect != image.Rect(-1, 9, 0, 10) || fixture.calls != 1 {
		t.Fatalf("verified result=%v err=%v calls=%d", matches, err, fixture.calls)
	}
	tests := []struct {
		name      string
		backend   Screenshotter
		area      image.Rectangle
		reference image.Image
		threshold float64
		limit     int
		want      error
	}{
		{"bad limit", fixture, area, ref, 1, 0, ErrInvalidFrame},
		{"nil screenshotter", nil, area, ref, 1, 64, util.ErrNotAvailable},
		{"invalid area", fixture, image.Rectangle{}, ref, 1, 64, ErrEmptyRegion},
		{"unverified", &templateUnverifiedFixture{frame: src}, area, ref, 1, 64, ErrUnverifiedCaptureGeometry},
		{"clipped", &templateVerifiedFixture{capture: VerifiedTemplateCapture{Image: src, DesktopRect: area, PixelScale: 1}}, area, ref, 1, 64, ErrUnverifiedCaptureGeometry},
		{"grab error", &templateVerifiedFixture{err: context.DeadlineExceeded}, area, ref, 1, 64, context.DeadlineExceeded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchTemplates(context.Background(), tc.backend, tc.area, tc.reference, tc.threshold, tc.limit)
			if !errors.Is(err, tc.want) || got != nil {
				t.Fatalf("got=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := MatchTemplates(ctx, fixture, area, ref, 1, 64)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("canceled result=%v err=%v", got, err)
	}
}

type cancelingTemplateBackend struct {
	templateVerifiedFixture
	cancel context.CancelFunc
}

func (b *cancelingTemplateBackend) GrabVerified(ctx context.Context, rect image.Rectangle) (VerifiedTemplateCapture, error) {
	capture, err := b.templateVerifiedFixture.GrabVerified(ctx, rect)
	b.cancel()
	return capture, err
}
func TestMatchTemplatesRejectsRevokedCaptureAndMalformedSources(t *testing.T) {
	area := image.Rect(0, 0, 2, 1)
	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{A: 255, R: 255})
	src := image.NewNRGBA(area)
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelingTemplateBackend{templateVerifiedFixture: templateVerifiedFixture{capture: VerifiedTemplateCapture{Image: src, DesktopRect: area, PixelScale: 1}}, cancel: cancel}
	result, err := MatchTemplates(ctx, backend, area, ref, 1, 64)
	if !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("late cancellation=%v %v", result, err)
	}
	oversized := image.NewNRGBA(image.Rect(0, 0, 1, 2))
	no, err := MatchTemplatesInImage(context.Background(), src, area, oversized, 1, 64)
	if err != nil || len(no) != 0 {
		t.Fatalf("oversized reference=%v %v", no, err)
	}
	_, err = MatchTemplatesInImage(context.Background(), nil, area, ref, 1, 64)
	if err == nil {
		t.Fatal("nil source should fail")
	}
	_, err = MatchTemplatesInImage(context.Background(), src, area, ref, -1, 64)
	if err == nil {
		t.Fatal("invalid threshold should fail")
	}
}

type templateCancelAtContext struct {
	context.Context
	calls int
	at    int
}

func (c *templateCancelAtContext) Err() error {
	c.calls++
	if c.calls >= c.at {
		return context.Canceled
	}
	return nil
}

func TestScanTemplateExcludingCancellationBoundaries(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	geometry := templateGeometry{searchArea: src.Bounds(), sourceW: 1, sourceH: 1, referenceW: 1, referenceH: 1, screenW: 1, screenH: 1}
	sample := templateSample{offset: 0, r: 0, g: 0, b: 0, weight: 255}
	for _, tc := range []struct {
		name      string
		at        int
		sample    templateSample
		threshold float64
	}{
		{"row", 1, sample, 1},
		{"exact match", 3, sample, 1},
		{"no match final", 3, templateSample{offset: 0, r: 255, weight: 255}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &templateCancelAtContext{Context: context.Background(), at: tc.at}
			got, err := scanTemplateExcluding(ctx, src, []templateSample{tc.sample}, 255, geometry, tc.threshold, nil)
			if !errors.Is(err, context.Canceled) || got != (TemplateMatch{}) {
				t.Fatalf("result=%v err=%v checks=%d", got, err, ctx.calls)
			}
		})
	}
	wide := image.NewNRGBA(image.Rect(0, 0, 1025, 1))
	wideGeometry := templateGeometry{searchArea: wide.Bounds(), sourceW: 1025, sourceH: 1, referenceW: 1025, referenceH: 1, screenW: 1025, screenH: 1}
	samples := make([]templateSample, 1025)
	for i := range samples {
		samples[i] = templateSample{offset: i * 4, weight: 255}
	}
	ctx := &templateCancelAtContext{Context: context.Background(), at: 3}
	got, err := scanTemplateExcluding(ctx, wide, samples, 255*1025, wideGeometry, 1, nil)
	if !errors.Is(err, context.Canceled) || got != (TemplateMatch{}) {
		t.Fatalf("diff cancellation=%v %v checks=%d", got, err, ctx.calls)
	}
}

func TestMatchTemplatesInImageCancelsBeforeReturningBoundedResults(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref := image.NewNRGBA(src.Bounds())
	ref.SetNRGBA(0, 0, color.NRGBA{A: 255})
	// Nine normalization/sample checks, three scan checks, then the final
	// return authority check after the requested result count is reached.
	ctx := &templateCancelAtContext{Context: context.Background(), at: 13}
	got, err := MatchTemplatesInImage(ctx, src, src.Bounds(), ref, 1, 1)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("final cancellation=%v %v checks=%d", got, err, ctx.calls)
	}
}

func TestMatchTemplatesInImageDropsMidScanCancellation(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref := image.NewNRGBA(src.Bounds())
	ref.SetNRGBA(0, 0, color.NRGBA{A: 255})
	ctx := &templateCancelAtContext{Context: context.Background(), at: 10}
	result, err := MatchTemplatesInImage(ctx, src, src.Bounds(), ref, 1, 64)
	if !errors.Is(err, context.Canceled) || result != nil {
		t.Fatalf("midscan cancellation=%v %v checks=%d", result, err, ctx.calls)
	}
}
