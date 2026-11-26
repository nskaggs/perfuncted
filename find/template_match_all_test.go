package find

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

// BenchmarkMatchTemplatesInImage measures the full-HD negative case and
// bounded dense all-matches retrieval without desktop capture overhead.
func BenchmarkMatchTemplatesInImage(b *testing.B) {
	for _, tc := range []struct {
		name string
		size image.Point
		fill bool
	}{
		{"fullHD-no-match", image.Pt(1920, 1080), false},
		{"dense-64-matches", image.Pt(128, 64), true},
		{"fullHD-dense-64", image.Pt(1920, 1080), true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			src := image.NewNRGBA(image.Rect(0, 0, tc.size.X, tc.size.Y))
			ref := image.NewNRGBA(image.Rect(0, 0, 4, 4))
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					ref.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
				}
			}
			if tc.fill {
				for y := 0; y < tc.size.Y; y++ {
					for x := 0; x < tc.size.X; x++ {
						src.SetNRGBA(x, y, color.NRGBA{R: 254, A: 255})
					}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				matches, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, 0.99, 64)
				if err != nil {
					b.Fatal(err)
				}
				if tc.fill && len(matches) != 64 {
					b.Fatalf("dense count=%d", len(matches))
				}
				if !tc.fill && len(matches) != 0 {
					b.Fatalf("unexpected negative matches=%d", len(matches))
				}
			}
		})
	}
}

// This deliberately uses the original repeated best-match implementation as
// a reference oracle, rather than reproducing the optimized collector's
// selection rules or trusting only a few hand-picked result positions.
func templateRepeatedScanOracle(t *testing.T, src, ref image.Image, area image.Rectangle, threshold float64, k int) []TemplateMatch {
	t.Helper()
	source, needle, g, err := prepareTemplateImages(context.Background(), src, ref, area)
	if err != nil {
		t.Fatal(err)
	}
	samples, weight, err := buildTemplateSamples(context.Background(), source.Stride, needle)
	if err != nil {
		t.Fatal(err)
	}
	matches := make([]TemplateMatch, 0, k)
	for len(matches) < k {
		candidate, err := scanTemplateExcluding(context.Background(), source, samples, weight, g, threshold, matches)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		matches = append(matches, candidate)
	}
	return matches
}

func TestTemplateSinglePassMatchesOriginalGreedyOracle(t *testing.T) {
	random := rand.New(rand.NewSource(126))
	for iteration := 0; iteration < 90; iteration++ {
		scale := 1 + iteration%2
		sourceWidth, sourceHeight := 8*scale, 6*scale
		src := image.NewNRGBA(image.Rect(0, 0, sourceWidth, sourceHeight))
		rw, rh := 1+random.Intn(min(5, sourceWidth)), 1+random.Intn(min(4, sourceHeight))
		ref := image.NewNRGBA(image.Rect(0, 0, rw, rh))
		for y := 0; y < sourceHeight; y++ {
			for x := 0; x < sourceWidth; x++ {
				c := color.NRGBA{R: uint8(random.Intn(4) * 70), G: uint8(random.Intn(4) * 70), B: uint8(random.Intn(4) * 70), A: 255}
				src.SetNRGBA(x, y, c)
			}
		}
		for y := 0; y < rh; y++ {
			for x := 0; x < rw; x++ {
				ref.SetNRGBA(x, y, color.NRGBA{R: uint8(random.Intn(4) * 70), G: uint8(random.Intn(4) * 70), B: uint8(random.Intn(4) * 70), A: uint8(random.Intn(4) * 85)})
			}
		}
		// Leave at least one pixel visible even for sparse alpha masks.
		ref.SetNRGBA(0, 0, color.NRGBA{R: 70, G: 140, B: 210, A: 255})
		area := image.Rect(-40, 15, -40+sourceWidth/scale, 15+sourceHeight/scale)
		for _, threshold := range []float64{0, .7, 1} {
			for _, limit := range []int{1, 2, 8, 64} {
				want := templateRepeatedScanOracle(t, src, ref, area, threshold, limit)
				got, err := MatchTemplatesInImage(context.Background(), src, area, ref, threshold, limit)
				if err != nil || len(got) != len(want) {
					t.Fatalf("case=%d scale=%d threshold=%v limit=%d got=%v err=%v want=%v", iteration, scale, threshold, limit, got, err, want)
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("case=%d scale=%d threshold=%v limit=%d index=%d got=%v want=%v", iteration, scale, threshold, limit, i, got, want)
					}
				}
			}
		}
	}
}

func TestTemplateRetentionIncludesSuppressedHigherRankedCandidates(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 200, 1))
	ref := image.NewNRGBA(image.Rect(0, 0, 80, 1))
	for x := 0; x < 200; x++ {
		src.SetNRGBA(x, 0, color.NRGBA{R: 255, A: 255})
	}
	for x := 0; x < 80; x++ {
		ref.SetNRGBA(x, 0, color.NRGBA{R: 255, A: 255})
	}
	got, err := MatchTemplatesInImage(context.Background(), src, src.Bounds(), ref, 1, 2)
	if err != nil || len(got) != 2 || got[0].Rect.Min.X != 0 || got[1].Rect.Min.X != 80 {
		t.Fatalf("selection after overlapping top ranks: %v %v", got, err)
	}
	g := templateGeometry{sourceW: 200, sourceH: 1, referenceW: 80, referenceH: 1, screenW: 200, screenH: 1}
	if n := templateRetentionLimit(g, 2); n != 121 {
		t.Fatalf("bounded by total possible placements = %d", n)
	}
	g = templateGeometry{sourceW: 1920, sourceH: 1080, referenceW: 4, referenceH: 4, screenW: 1920, screenH: 1080}
	if n := templateRetentionLimit(g, 64); n != 3136 {
		t.Fatalf("4x4 overlap bound = %d, want 3136", n)
	}
	// At 2x, placements sharing the same desktop rectangle can overlap
	// even if their pixel template intervals do not.
	g = templateGeometry{sourceW: 16, sourceH: 12, referenceW: 1, referenceH: 1, screenW: 8, screenH: 6}
	if n := templateRetentionLimit(g, 2); n != 18 {
		t.Fatalf("2x conservative bound = %d, want 18", n)
	}
}

func TestTemplateHeapRankTiesAndBoundedCapacity(t *testing.T) {
	var heap templateRankHeap
	for _, candidate := range []templateRank{
		{confidence: .9, index: 8}, {confidence: .8, index: 3}, {confidence: .9, index: 2},
		{confidence: 1, index: 9}, {confidence: 1, index: 1}, {confidence: .95, index: 4},
	} {
		heap = heap.pushTop(candidate, 3)
	}
	if len(heap) != 3 {
		t.Fatalf("retained = %v cap=%d", heap, cap(heap))
	}
	if err := heap.buildBestFirst(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []templateRank{{confidence: 1, index: 1}, {confidence: 1, index: 9}, {confidence: .95, index: 4}}
	for i, expected := range want {
		var got templateRank
		got, heap = heap.popBest()
		if got != expected {
			t.Fatalf("rank %d: got=%v want=%v", i, got, expected)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := heap.buildBestFirst(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled heap rebuild = %v", err)
	}
	big := make(templateRankHeap, 2049)
	ctx = &templateCancelAtContext{Context: context.Background(), at: 1}
	if err := big.buildBestFirst(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("midbuild cancellation = %v", err)
	}
}

func TestTemplateSinglePassCancellationDuringScan(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1025, 2))
	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{A: 255})
	ctx := &templateCancelAtContext{Context: context.Background(), at: 25}
	matches, err := MatchTemplatesInImage(ctx, src, src.Bounds(), ref, 0, 64)
	if !errors.Is(err, context.Canceled) || matches != nil {
		t.Fatalf("canceled: %v %v, calls=%d", matches, err, ctx.calls)
	}
}

// A context that starts failing after a prescribed number of authority checks
// exercises the scanner's admission, post-scan, heap-build, extraction and
// final-return checks without races or time-based sleeps.
func TestTemplateSinglePassCancellationAtEveryPhase(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	geometry := templateGeometry{searchArea: source.Bounds(), sourceW: 1, sourceH: 1, referenceW: 1, referenceH: 1, screenW: 1, screenH: 1}
	samples := []templateSample{{offset: 0, weight: 255}}
	for _, at := range []int{1, 2, 3, 4, 5, 6} {
		t.Run(fmt.Sprintf("authority-check-%d", at), func(t *testing.T) {
			ctx := &templateCancelAtContext{Context: context.Background(), at: at}
			got, err := collectTemplateMatches(ctx, source, samples, 255, geometry, 0, 1)
			if !errors.Is(err, context.Canceled) || got != nil {
				t.Fatalf("at=%d checks=%d got=%v err=%v", at, ctx.calls, got, err)
			}
		})
	}
	wide := image.NewNRGBA(image.Rect(0, 0, 1025, 1))
	bigGeometry := templateGeometry{searchArea: wide.Bounds(), sourceW: 1025, sourceH: 1, referenceW: 1025, referenceH: 1, screenW: 1025, screenH: 1}
	manySamples := make([]templateSample, 1025)
	for i := range manySamples {
		manySamples[i] = templateSample{offset: i * 4, weight: 255}
	}
	ctx := &templateCancelAtContext{Context: context.Background(), at: 3}
	got, err := collectTemplateMatches(ctx, wide, manySamples, 255*1025, bigGeometry, 0, 1)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("pixel comparison cancellation: got=%v err=%v", got, err)
	}
}

func TestTemplateSinglePassRejectsRoundedThresholdCandidate(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	geometry := templateGeometry{searchArea: source.Bounds(), sourceW: 1, sourceH: 1, referenceW: 1, referenceH: 1, screenW: 1, screenH: 1}
	samples := []templateSample{{offset: 0, r: 1, weight: 255}}
	// Difference 255 exactly meets the upward-rounded prune budget, but the
	// actual confidence is strictly below this threshold.
	threshold := 1 - 254.5/float64(255*765)
	matches, err := collectTemplateMatches(context.Background(), source, samples, 255, geometry, threshold, 1)
	if err != nil || len(matches) != 0 {
		t.Fatalf("rounded threshold=%v err=%v", matches, err)
	}
}

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
