package find

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
)

func TestReviewOCRExactWhitespaceRejectedBeforeCapture(t *testing.T) {
	for _, mode := range []TextMatchMode{TextMatchExact, TextMatchNormalizeCase} {
		for _, query := range []string{"two  words", " leading", "trailing ", "two\twords"} {
			t.Run(string(mode)+"/"+query, func(t *testing.T) {
				area := image.Rect(0, 0, 10, 10)
				backend := &textTestCapture{capture: VerifiedTemplateCapture{Image: image.NewNRGBA(area), DesktopRect: area, PixelScale: 1}}
				provider := &textTestRecognizer{}
				_, err := RecognizeText(context.Background(), backend, area, query, TextOptions{Recognizer: provider, MatchMode: mode})
				if !errors.Is(err, ErrInvalidFrame) || backend.calls != 0 || provider.calls != 0 {
					t.Fatalf("err=%v capture=%d recognition=%d; invalid query must fail before side effects", err, backend.calls, provider.calls)
				}
			})
		}
	}
}

func TestReviewTesseractLiteralQuotes(t *testing.T) {
	for _, word := range []string{`"hello"`, `5"wide`, `"unclosed`, `"`} {
		t.Run(word, func(t *testing.T) {
			data := validTesseractHeader + "5\t1\t1\t1\t1\t1\t0\t0\t5\t5\t90\t" + word + "\n"
			got, err := parseTesseractTSV([]byte(data), 10, 10)
			if err != nil || len(got) != 1 || got[0].Text != word {
				t.Fatalf("word=%q got=%+v err=%v", word, got, err)
			}
		})
	}
}

func TestReviewTesseractTSVCRLF(t *testing.T) {
	data := strings.ReplaceAll(validTesseractHeader, "\n", "\r\n") +
		"5\t1\t1\t1\t1\t1\t0\t0\t5\t5\t90\t\"hello\"\r\n"
	got, err := parseTesseractTSV([]byte(data), 10, 10)
	if err != nil || len(got) != 1 || got[0].Text != `"hello"` {
		t.Fatalf("CRLF TSV got=%+v err=%v", got, err)
	}
}
