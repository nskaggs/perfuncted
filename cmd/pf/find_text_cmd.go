package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/spf13/cobra"
)

type textRecognizerFactory func(executable, language string) (find.OCRRecognizer, error)

func findTextCmd(openPF sessionOpener, newRecognizer textRecognizerFactory) *cobra.Command {
	var query, rectFlag, providerName, language, matchMode, output, timeoutFlag string
	var maxResults int
	cmd := &cobra.Command{
		Use:   "text",
		Short: "Find rendered text using an explicitly selected local OCR provider",
		Long: `Capture one authorized screen region and recognize its visible text with an
explicitly selected local provider. Tesseract is optional and is never enabled
implicitly. The provider runs locally without a shell, network fallback, or
temporary screenshot files. Confidence is provider-specific, not a probability.
No matching text returns an empty successful result. Use --output json for
machine-readable matches with desktop and capture-pixel rectangles. Tesseract
verifies the selected language pack before screen capture.

Matches are contiguous OCR words on one line and are ordered top-to-bottom,
then left-to-right. Exact matching is case-sensitive and requires one ASCII
space between words. normalize-case lowercases Unicode text while preserving
space separators; normalize-whitespace collapses Unicode whitespace without
changing case; normalize applies both. JSON reports screen coordinates and
image-pixel coordinates, the source rectangle and whether --max-results
truncated the result. Tesseract confidence is on its 0–100 provider scale.`,
		Example: "pf find text --query 'Continue' --rect 0,0,1280,720 --provider tesseract\npf find text --query 'Invoice paid' --rect 0,0,1280,720 --provider tesseract --match normalize --output json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if providerName != "tesseract" {
				return fmt.Errorf("unsupported OCR provider %q (supported: tesseract)", providerName)
			}
			rect, err := parseRect(rectFlag)
			if err != nil {
				return err
			}
			if rect.Empty() {
				return fmt.Errorf("--rect must have positive width and height")
			}
			timeout, err := parseDuration(timeoutFlag, 5*time.Second)
			if err != nil {
				return err
			}
			if output != "plain" && output != "json" {
				return fmt.Errorf("--output must be plain or json")
			}
			if maxResults < 1 || maxResults > 1024 {
				return fmt.Errorf("--max-results must be in [1,1024]")
			}
			options := find.TextOptions{MatchMode: find.TextMatchMode(matchMode), MaxResults: maxResults}
			if query == "" {
				return fmt.Errorf("--query must not be empty")
			}
			// Resolve the optional executable before opening the desktop session so a
			// missing provider cannot trigger capture or permission side effects.
			recognizer, err := newRecognizer("", language)
			if err != nil {
				return err
			}
			options.Recognizer = recognizer
			if validationErr := find.ValidateTextOptions(query, options); validationErr != nil {
				return validationErr
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			pf, err := openPF(ctx)
			if err != nil {
				return err
			}
			defer pf.Close()
			result, err := pf.Screen.FindText(ctx, rect, query, options)
			if err != nil {
				return err
			}
			return writeTextMatches(cmd.OutOrStdout(), output, result)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "text phrase to find")
	cmd.Flags().StringVar(&rectFlag, "rect", "", "capture region as x0,y0,x1,y1")
	cmd.Flags().StringVar(&providerName, "provider", "", "explicit local OCR provider (tesseract)")
	cmd.Flags().StringVar(&language, "language", "eng", "Tesseract language code or combination")
	cmd.Flags().StringVar(&matchMode, "match", string(find.TextMatchExact), "exact|normalize-case|normalize-whitespace|normalize")
	cmd.Flags().StringVar(&output, "output", "plain", "plain|json")
	cmd.Flags().IntVar(&maxResults, "max-results", 256, "maximum matches to return (1..1024)")
	cmd.Flags().StringVar(&timeoutFlag, "timeout", "5s", "capture and OCR timeout")
	_ = cmd.MarkFlagRequired("query")
	_ = cmd.MarkFlagRequired("rect")
	_ = cmd.MarkFlagRequired("provider")
	return cmd
}

func writeTextMatches(w io.Writer, output string, result find.TextResult) error {
	if output == "json" {
		return json.NewEncoder(w).Encode(result)
	}
	if len(result.Matches) == 0 {
		return writeCLIOutput(w, "no match\n")
	}
	for _, match := range result.Matches {
		if match.Confidence == nil {
			if err := writeCLIOutput(w, "match text=%q x=%d y=%d width=%d height=%d\n", match.Text, match.Rect.Min.X, match.Rect.Min.Y, match.Rect.Dx(), match.Rect.Dy()); err != nil {
				return err
			}
		} else if err := writeCLIOutput(w, "match text=%q x=%d y=%d width=%d height=%d confidence=%.3f\n", match.Text, match.Rect.Min.X, match.Rect.Min.Y, match.Rect.Dx(), match.Rect.Dy(), *match.Confidence); err != nil {
			return err
		}
	}
	if result.Truncated {
		return writeCLIOutput(w, "warning: result limit reached; use --max-results to raise it\n")
	}
	return nil
}

func defaultTextRecognizer(executable, language string) (find.OCRRecognizer, error) {
	return find.NewTesseractProvider(executable, language)
}
