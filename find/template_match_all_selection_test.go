package find

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"testing"
)

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
