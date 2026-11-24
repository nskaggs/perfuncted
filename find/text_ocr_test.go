package find

import (
	"context"
	"errors"
	"image"
	"math"
	"testing"
)

type textTestCapture struct {
	capture VerifiedTemplateCapture
	err     error
	calls   int
}

func (s *textTestCapture) GrabVerified(_ context.Context, area image.Rectangle) (VerifiedTemplateCapture, error) {
	s.calls++
	if s.err != nil {
		return VerifiedTemplateCapture{}, s.err
	}
	if s.capture.DesktopRect != area {
		return VerifiedTemplateCapture{}, ErrUnverifiedCaptureGeometry
	}
	return s.capture, nil
}

type textTestRecognizer struct {
	words  []OCRWord
	err    error
	calls  int
	cancel context.CancelFunc
}

func (*textTestRecognizer) Name() string     { return "fixture" }
func (*textTestRecognizer) Language() string { return "eng" }
func (r *textTestRecognizer) Recognize(_ context.Context, _ image.Image) ([]OCRWord, error) {
	r.calls++
	if r.cancel != nil {
		r.cancel()
	}
	return r.words, r.err
}

func TestFindTextMapsNormalizedPhraseAndOrdersMultipleMatches(t *testing.T) {
	area := image.Rect(100, 200, 120, 220)
	img := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	confidence := 90.0
	words := []OCRWord{
		{Text: "Invoice", PixelRect: image.Rect(4, 4, 20, 14), Confidence: &confidence, Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: 1},
		{Text: "PAID", PixelRect: image.Rect(22, 4, 34, 14), Confidence: &confidence, Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: 2},
		{Text: "Invoice", PixelRect: image.Rect(4, 28, 20, 38), Confidence: &confidence, Page: 1, Block: 1, Paragraph: 1, Line: 2, Word: 1},
		{Text: "PAID", PixelRect: image.Rect(22, 28, 34, 38), Confidence: &confidence, Page: 1, Block: 1, Paragraph: 1, Line: 2, Word: 2},
	}
	backend := &textTestCapture{capture: VerifiedTemplateCapture{Image: img, DesktopRect: area, PixelScale: 2}}
	provider := &textTestRecognizer{words: words}
	got, err := RecognizeText(context.Background(), backend, area, "Invoice   paid", TextOptions{Recognizer: provider, MatchMode: TextMatchNormalize, MaxResults: 4})
	if err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 || provider.calls != 1 || len(got.Matches) != 2 || got.Truncated {
		t.Fatalf("calls=%d/%d result=%+v", backend.calls, provider.calls, got)
	}
	if got.Provider != "fixture" || got.Language != "eng" || got.SourceRect != area {
		t.Fatalf("provenance=%+v", got)
	}
	wantRects := []image.Rectangle{image.Rect(102, 202, 117, 207), image.Rect(102, 214, 117, 219)}
	for i, want := range wantRects {
		if got.Matches[i].Rect != want {
			t.Fatalf("match %d rect=%v want %v", i, got.Matches[i].Rect, want)
		}
		if got.Matches[i].Text != "Invoice PAID" || got.Matches[i].Confidence == nil || *got.Matches[i].Confidence != 90 {
			t.Fatalf("match %d=%+v", i, got.Matches[i])
		}
	}
}

func TestFindTextExactNoMatchAndResultLimit(t *testing.T) {
	area := image.Rect(0, 0, 20, 20)
	img := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	words := make([]OCRWord, 0, 3)
	for i := 0; i < 3; i++ {
		words = append(words, OCRWord{Text: "Go", PixelRect: image.Rect(i*6, 0, i*6+4, 5), Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: i + 1})
	}
	backend := &textTestCapture{capture: VerifiedTemplateCapture{Image: img, DesktopRect: area, PixelScale: 1}}
	provider := &textTestRecognizer{words: words}
	got, err := RecognizeText(context.Background(), backend, area, "Go", TextOptions{Recognizer: provider, MatchMode: TextMatchExact, MaxResults: 2})
	if err != nil || len(got.Matches) != 2 || !got.Truncated {
		t.Fatalf("limited=%+v err=%v", got, err)
	}
	got, err = RecognizeText(context.Background(), backend, area, "go", TextOptions{Recognizer: provider, MatchMode: TextMatchExact})
	if err != nil || len(got.Matches) != 0 || got.Matches == nil {
		t.Fatalf("no-match=%+v err=%v", got, err)
	}
	got, err = RecognizeText(context.Background(), backend, area, "Go", TextOptions{Recognizer: provider, MatchMode: TextMatchNormalizeCase})
	if err != nil || len(got.Matches) != 3 {
		t.Fatalf("normalized=%+v err=%v", got, err)
	}
}

func TestFindTextRejectsProviderAndGeometryFailuresBeforeReturningMatches(t *testing.T) {
	area := image.Rect(0, 0, 4, 4)
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	provider := &textTestRecognizer{words: []OCRWord{{Text: "bad", PixelRect: image.Rect(0, 0, 0, 2)}}}
	backend := &textTestCapture{capture: VerifiedTemplateCapture{Image: img, DesktopRect: area, PixelScale: 1}}
	if _, err := RecognizeText(context.Background(), backend, area, "x", TextOptions{}); !errors.Is(err, ErrOCRProviderUnavailable) || backend.calls != 0 {
		t.Fatalf("provider preflight err=%v capture calls=%d", err, backend.calls)
	}
	if _, err := RecognizeText(context.Background(), backend, area, "x", TextOptions{Recognizer: provider}); !errors.Is(err, ErrOCRRecognition) {
		t.Fatalf("invalid word error=%v", err)
	}
	provider.err = errors.New("fixture recognition failed")
	provider.words = nil
	if _, err := RecognizeText(context.Background(), backend, area, "x", TextOptions{Recognizer: provider}); !errors.Is(err, ErrOCRRecognition) {
		t.Fatalf("provider error=%v", err)
	}
	backend.capture.PixelScale = 0
	if _, err := RecognizeText(context.Background(), backend, area, "x", TextOptions{Recognizer: provider}); !errors.Is(err, ErrOCRGeometry) {
		t.Fatalf("geometry error=%v", err)
	}
	backend.capture = VerifiedTemplateCapture{Image: image.NewNRGBA(image.Rect(0, 0, 2, 4)), DesktopRect: area, PixelScale: 1}
	if _, err := RecognizeText(context.Background(), backend, area, "x", TextOptions{Recognizer: provider}); !errors.Is(err, ErrOCRGeometry) || provider.calls != 2 {
		t.Fatalf("clipped capture error=%v recognizer calls=%d", err, provider.calls)
	}
}

func TestFindTextRejectsInvalidBoundsConfidenceAndQueryBeforeCapture(t *testing.T) {
	area := image.Rect(0, 0, 10, 10)
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	backend := &textTestCapture{capture: VerifiedTemplateCapture{Image: img, DesktopRect: area, PixelScale: 1}}
	provider := &textTestRecognizer{words: []OCRWord{{Text: "x", PixelRect: image.Rect(9, 9, 11, 11)}}}
	if _, err := RecognizeText(context.Background(), backend, area, " \t", TextOptions{Recognizer: provider}); !errors.Is(err, ErrInvalidFrame) || backend.calls != 0 {
		t.Fatalf("query preflight err=%v calls=%d", err, backend.calls)
	}
	if _, err := RecognizeText(context.Background(), backend, area, "x", TextOptions{Recognizer: provider}); !errors.Is(err, ErrOCRRecognition) {
		t.Fatalf("bounds error=%v", err)
	}
	confidence := math.NaN()
	provider.words = []OCRWord{{Text: "x", PixelRect: image.Rect(0, 0, 1, 1), Confidence: &confidence}}
	if _, err := RecognizeText(context.Background(), backend, area, "x", TextOptions{Recognizer: provider}); !errors.Is(err, ErrOCRRecognition) {
		t.Fatalf("confidence error=%v", err)
	}
}

func TestFindTextInImageUsesImagePixelCoordinatesWithoutScreenCapture(t *testing.T) {
	img := image.NewNRGBA(image.Rect(5, 7, 15, 17))
	provider := &textTestRecognizer{words: []OCRWord{{Text: "canvas", PixelRect: image.Rect(2, 3, 8, 6), Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: 1}}}
	got, err := RecognizeTextInImage(context.Background(), img, "canvas", TextOptions{Recognizer: provider})
	if err != nil {
		t.Fatal(err)
	}
	if got.CoordinateSpace != "image-pixels" || got.SourceRect != image.Rect(0, 0, 10, 10) || len(got.Matches) != 1 || got.Matches[0].Rect != image.Rect(2, 3, 8, 6) {
		t.Fatalf("result=%+v", got)
	}
}

func TestRecognizeTextDiscardsProviderOutputAfterCancellation(t *testing.T) {
	area := image.Rect(0, 0, 2, 2)
	img := image.NewNRGBA(area)
	backend := &textTestCapture{capture: VerifiedTemplateCapture{Image: img, DesktopRect: area, PixelScale: 1}}
	ctx, cancel := context.WithCancel(context.Background())
	provider := &textTestRecognizer{words: []OCRWord{{Text: "secret", PixelRect: image.Rect(0, 0, 1, 1)}}, cancel: cancel}
	got, err := RecognizeText(ctx, backend, area, "secret", TextOptions{Recognizer: provider})
	if !errors.Is(err, context.Canceled) || len(got.Matches) != 0 || got.Provider != "" {
		t.Fatalf("result=%+v error=%v", got, err)
	}
}
