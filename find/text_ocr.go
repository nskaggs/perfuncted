package find

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nskaggs/perfuncted/v2/internal/contextutil"
	"github.com/nskaggs/perfuncted/v2/internal/util"
)

var (
	// ErrOCRProviderUnavailable reports that no explicitly selected local provider or language is available.
	ErrOCRProviderUnavailable = errors.New("find: OCR provider unavailable")
	// ErrOCRRecognition reports a failed or malformed provider response.
	ErrOCRRecognition = errors.New("find: OCR recognition failed")
	// ErrOCRGeometry reports that OCR pixel boxes cannot be mapped to trusted source coordinates.
	ErrOCRGeometry = errors.New("find: OCR capture geometry is unverified")
)

const (
	maxOCRWords       = 16_384
	maxOCRWordBytes   = 4_096
	maxOCRMatches     = 1_024
	defaultOCRMatches = 256
	maxOCRImagePixels = 16 * 1024 * 1024
)

// TextMatchMode controls how a query is compared with consecutive recognized
// words. Exact preserves case and the single spaces used between OCR words.
// The normalized modes optionally lowercase Unicode text and/or collapse
// Unicode whitespace to one ASCII space.
type TextMatchMode string

const (
	// TextMatchExact preserves case and single-space OCR token separators.
	TextMatchExact TextMatchMode = "exact"
	// TextMatchNormalizeCase lowercases Unicode text while preserving whitespace.
	TextMatchNormalizeCase TextMatchMode = "normalize-case"
	// TextMatchNormalizeWhitespace collapses Unicode whitespace without changing case.
	TextMatchNormalizeWhitespace TextMatchMode = "normalize-whitespace"
	// TextMatchNormalize lowercases Unicode text and collapses Unicode whitespace.
	TextMatchNormalize TextMatchMode = "normalize"
)

// OCRWord is one provider-recognized word in image pixel coordinates. The
// provider must return half-open, non-empty bounds inside the supplied image.
// Confidence, when present, uses that provider's 0..100 scale.
type OCRWord struct {
	Text       string
	PixelRect  image.Rectangle
	Confidence *float64
	Page       int
	Block      int
	Paragraph  int
	Line       int
	Word       int
}

// OCRRecognizer is an explicitly selected local recognition provider.
type OCRRecognizer interface {
	Name() string
	Language() string
	Recognize(context.Context, image.Image) ([]OCRWord, error)
}

// TextOptions selects the OCR provider and matching behavior. Exact matching
// is case-sensitive; the normalize modes control Unicode lowercasing and
// whitespace folding. MaxResults is bounded to limit repeated-label output.
type TextOptions struct {
	Recognizer OCRRecognizer
	MatchMode  TextMatchMode
	MaxResults int
}

// TextMatch contains one matched phrase. Rect uses the coordinate space named
// by TextResult.CoordinateSpace; PixelRect always uses captured-image pixels.
// Confidence is the mean of word confidences when every word provides one.
type TextMatch struct {
	Text       string          `json:"text"`
	Rect       image.Rectangle `json:"rect"`
	PixelRect  image.Rectangle `json:"pixel_rect"`
	Confidence *float64        `json:"confidence,omitempty"`
}

// TextResult records the requested capture provenance and its text matches.
// An empty Matches slice with nil error is a successful recognition with no
// query match. Truncated indicates that MaxResults limited the returned list.
type TextResult struct {
	Provider        string          `json:"provider"`
	Language        string          `json:"language"`
	CoordinateSpace string          `json:"coordinate_space"`
	SourceRect      image.Rectangle `json:"source_rect"`
	Matches         []TextMatch     `json:"matches"`
	Truncated       bool            `json:"truncated,omitempty"`
}

// RecognizeText captures a complete, backend-attested region once, runs the
// explicitly supplied OCR provider, and maps matching word boxes to desktop
// coordinates. Backends that cannot attest exact coverage and integer pixel
// scale fail closed.
func RecognizeText(ctx context.Context, sc VerifiedTemplateScreenshotter, area image.Rectangle, query string, options TextOptions) (TextResult, error) {
	ctx = contextutil.Default(ctx)
	if options.MatchMode == "" {
		options.MatchMode = TextMatchExact
	}
	if err := ValidateTextOptions(query, options); err != nil {
		return TextResult{}, err
	}
	if err := contextErr(ctx); err != nil {
		return TextResult{}, err
	}
	if area.Empty() {
		return TextResult{}, fmt.Errorf("find: OCR search area is empty: %w", ErrInvalidFrame)
	}
	if util.IsNil(sc) {
		return TextResult{}, fmt.Errorf("find: OCR screen backend %w", ErrOCRGeometry)
	}
	capture, err := sc.GrabVerified(ctx, area)
	if err != nil {
		return TextResult{}, fmt.Errorf("find: OCR capture: %w", err)
	}
	if err := contextErr(ctx); err != nil {
		return TextResult{}, err
	}
	if err := validateVerifiedTemplateCapture(capture, area); err != nil {
		return TextResult{}, fmt.Errorf("%w: %w", ErrOCRGeometry, err)
	}
	if err := validateOCRImage(capture.Image); err != nil {
		return TextResult{}, err
	}
	return recognizeText(ctx, capture, query, options, "desktop")
}

// RecognizeTextInImage recognizes and searches an image supplied by the caller.
// It does not open a desktop session or require capture permissions. Returned
// Rect values use image-pixel coordinates and CoordinateSpace is "image-pixels".
func RecognizeTextInImage(ctx context.Context, img image.Image, query string, options TextOptions) (TextResult, error) {
	ctx = contextutil.Default(ctx)
	if options.MatchMode == "" {
		options.MatchMode = TextMatchExact
	}
	if err := ValidateTextOptions(query, options); err != nil {
		return TextResult{}, err
	}
	if err := contextErr(ctx); err != nil {
		return TextResult{}, err
	}
	if err := validateOCRImage(img); err != nil {
		return TextResult{}, err
	}
	bounds := img.Bounds()
	capture := VerifiedTemplateCapture{Image: img, DesktopRect: image.Rect(0, 0, bounds.Dx(), bounds.Dy()), PixelScale: 1}
	return recognizeText(ctx, capture, query, options, "image-pixels")
}

func validateOCRImage(img image.Image) error {
	if err := checkImage(img, "OCR input"); err != nil {
		return fmt.Errorf("%w: %w", ErrOCRGeometry, err)
	}
	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	if width <= 0 || height <= 0 || width > 8192 || height > 8192 || width > maxOCRImagePixels/height {
		return fmt.Errorf("find: OCR image dimensions %dx%d exceed supported limits: %w", width, height, ErrInvalidFrame)
	}
	return nil
}

func recognizeText(ctx context.Context, capture VerifiedTemplateCapture, query string, options TextOptions, coordinateSpace string) (TextResult, error) {
	words, err := options.Recognizer.Recognize(ctx, capture.Image)
	if err != nil {
		if ctxErr := contextErr(ctx); ctxErr != nil {
			return TextResult{}, ctxErr
		}
		return TextResult{}, fmt.Errorf("%w (%s): %w", ErrOCRRecognition, options.Recognizer.Name(), err)
	}
	if err := contextErr(ctx); err != nil {
		return TextResult{}, err
	}
	if len(words) > maxOCRWords {
		return TextResult{}, fmt.Errorf("find: OCR provider returned %d words (limit %d): %w", len(words), maxOCRWords, ErrOCRRecognition)
	}
	return matchOCRWords(ctx, words, capture, options.Recognizer, query, options, coordinateSpace)
}

// ValidateTextOptions performs side-effect-free OCR request validation. It is
// exported so a session facade can reject malformed requests before capture.
func ValidateTextOptions(query string, options TextOptions) error {
	if strings.TrimSpace(query) == "" || len(query) > maxOCRWordBytes || !utf8.ValidString(query) || strings.ContainsRune(query, '\x00') {
		return fmt.Errorf("find: OCR query must be non-empty valid UTF-8 without NUL: %w", ErrInvalidFrame)
	}
	if util.IsNil(options.Recognizer) {
		return fmt.Errorf("%w: select a local OCR recognizer explicitly", ErrOCRProviderUnavailable)
	}
	if options.Recognizer.Name() == "" || options.Recognizer.Language() == "" {
		return fmt.Errorf("%w: recognizer must identify its provider and language", ErrOCRProviderUnavailable)
	}
	mode := options.MatchMode
	if mode == "" {
		mode = TextMatchExact
	}
	switch mode {
	case TextMatchExact, TextMatchNormalizeCase, TextMatchNormalizeWhitespace, TextMatchNormalize:
	default:
		return fmt.Errorf("find: unsupported OCR match mode %q: %w", options.MatchMode, ErrInvalidFrame)
	}
	if options.MaxResults < 0 || options.MaxResults > maxOCRMatches {
		return fmt.Errorf("find: OCR result limit must be in [0,%d]: %w", maxOCRMatches, ErrInvalidFrame)
	}
	queryWords, err := ocrQueryTokens(query, mode)
	if err != nil {
		return fmt.Errorf("find: invalid OCR query whitespace: %w", ErrInvalidFrame)
	}
	if len(queryWords) > 128 {
		return fmt.Errorf("find: OCR query may contain at most 128 words: %w", ErrInvalidFrame)
	}
	return nil
}

type ocrLineKey struct{ page, block, paragraph, line int }

func matchOCRWords(ctx context.Context, words []OCRWord, capture VerifiedTemplateCapture, recognizer OCRRecognizer, query string, options TextOptions, coordinateSpace string) (TextResult, error) {
	bounds := capture.Image.Bounds()
	lines, err := groupOCRWords(ctx, words, image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	if err != nil {
		return TextResult{}, err
	}
	queryTokens, err := ocrQueryTokens(query, options.MatchMode)
	if err != nil || len(queryTokens) == 0 {
		return TextResult{}, fmt.Errorf("find: OCR query is empty after normalization: %w", ErrInvalidFrame)
	}
	result := TextResult{Provider: recognizer.Name(), Language: recognizer.Language(), CoordinateSpace: coordinateSpace, SourceRect: capture.DesktopRect, Matches: []TextMatch{}}
	limit := options.MaxResults
	if limit == 0 {
		limit = defaultOCRMatches
	}
	for _, key := range sortedOCRLineKeys(lines) {
		matches, err := matchOCRLine(ctx, lines[key], capture, queryTokens, options.MatchMode)
		if err != nil {
			return TextResult{}, err
		}
		result.Matches = append(result.Matches, matches...)
	}
	sort.SliceStable(result.Matches, func(i, j int) bool {
		return ocrMatchLess(result.Matches[i], result.Matches[j])
	})
	if err := contextErr(ctx); err != nil {
		return TextResult{}, err
	}
	if len(result.Matches) > limit {
		result.Matches = slices.Clone(result.Matches[:limit])
		result.Truncated = true
	}
	return result, nil
}

func validOCRWord(word OCRWord, bounds image.Rectangle) bool {
	return utf8.ValidString(word.Text) && len(word.Text) <= maxOCRWordBytes &&
		strings.TrimSpace(word.Text) == word.Text && len(strings.Fields(word.Text)) == 1 &&
		!strings.ContainsRune(word.Text, '\x00') && word.Page >= 0 && word.Block >= 0 &&
		word.Paragraph >= 0 && word.Line >= 0 && word.Word >= 0 &&
		!word.PixelRect.Empty() && word.PixelRect.In(bounds)
}

func groupOCRWords(ctx context.Context, words []OCRWord, bounds image.Rectangle) (map[ocrLineKey][]OCRWord, error) {
	lines := make(map[ocrLineKey][]OCRWord)
	for _, word := range words {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		if !validOCRWord(word, bounds) {
			return nil, fmt.Errorf("%w: provider returned invalid text or pixel bounds", ErrOCRRecognition)
		}
		if word.Confidence != nil && (math.IsNaN(*word.Confidence) || math.IsInf(*word.Confidence, 0) || *word.Confidence < 0 || *word.Confidence > 100) {
			return nil, fmt.Errorf("%w: provider returned invalid confidence", ErrOCRRecognition)
		}
		key := ocrLineKey{word.Page, word.Block, word.Paragraph, word.Line}
		lines[key] = append(lines[key], word)
	}
	return lines, nil
}

func sortedOCRLineKeys(lines map[ocrLineKey][]OCRWord) []ocrLineKey {
	keys := make([]ocrLineKey, 0, len(lines))
	for key := range lines {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		liX, liY := lineTopLeft(lines[keys[i]])
		ljX, ljY := lineTopLeft(lines[keys[j]])
		if liY != ljY {
			return liY < ljY
		}
		if liX != ljX {
			return liX < ljX
		}
		if keys[i].page != keys[j].page {
			return keys[i].page < keys[j].page
		}
		if keys[i].block != keys[j].block {
			return keys[i].block < keys[j].block
		}
		if keys[i].paragraph != keys[j].paragraph {
			return keys[i].paragraph < keys[j].paragraph
		}
		return keys[i].line < keys[j].line
	})
	return keys
}

func matchOCRLine(ctx context.Context, line []OCRWord, capture VerifiedTemplateCapture, tokens []string, mode TextMatchMode) ([]TextMatch, error) {
	sort.SliceStable(line, func(i, j int) bool {
		if line[i].Word != line[j].Word {
			return line[i].Word < line[j].Word
		}
		if line[i].PixelRect.Min.X != line[j].PixelRect.Min.X {
			return line[i].PixelRect.Min.X < line[j].PixelRect.Min.X
		}
		return line[i].PixelRect.Min.Y < line[j].PixelRect.Min.Y
	})
	var matches []TextMatch
	for start := range line {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		if len(line)-start < len(tokens) {
			continue
		}
		match, ok, err := matchOCRSpan(line[start:start+len(tokens)], capture, tokens, mode)
		if err != nil {
			return nil, err
		}
		if ok {
			matches = append(matches, match)
		}
	}
	return matches, nil
}

func matchOCRSpan(words []OCRWord, capture VerifiedTemplateCapture, tokens []string, mode TextMatchMode) (TextMatch, bool, error) {
	textParts := make([]string, len(tokens))
	var pixelRect image.Rectangle
	var confidence float64
	confidenceKnown := true
	for offset, token := range tokens {
		word := words[offset]
		if normalizeOCRWord(word.Text, mode) != token {
			return TextMatch{}, false, nil
		}
		textParts[offset] = word.Text
		if offset == 0 {
			pixelRect = word.PixelRect
		} else {
			pixelRect = pixelRect.Union(word.PixelRect)
		}
		if word.Confidence == nil {
			confidenceKnown = false
		} else {
			confidence += *word.Confidence
		}
	}
	mapped, err := mapOCRRect(pixelRect, capture)
	if err != nil {
		return TextMatch{}, false, err
	}
	match := TextMatch{Text: strings.Join(textParts, " "), Rect: mapped, PixelRect: pixelRect}
	if confidenceKnown {
		mean := confidence / float64(len(tokens))
		match.Confidence = &mean
	}
	return match, true, nil
}

func ocrMatchLess(a, b TextMatch) bool {
	if a.Rect.Min.Y != b.Rect.Min.Y {
		return a.Rect.Min.Y < b.Rect.Min.Y
	}
	if a.Rect.Min.X != b.Rect.Min.X {
		return a.Rect.Min.X < b.Rect.Min.X
	}
	if a.Rect.Max.Y != b.Rect.Max.Y {
		return a.Rect.Max.Y < b.Rect.Max.Y
	}
	return a.Text < b.Text
}

func lineTopLeft(words []OCRWord) (x, y int) {
	if len(words) == 0 {
		return 0, 0
	}
	x, y = words[0].PixelRect.Min.X, words[0].PixelRect.Min.Y
	for _, word := range words[1:] {
		if word.PixelRect.Min.Y < y || word.PixelRect.Min.Y == y && word.PixelRect.Min.X < x {
			x, y = word.PixelRect.Min.X, word.PixelRect.Min.Y
		}
	}
	return x, y
}

func ocrQueryTokens(query string, mode TextMatchMode) ([]string, error) {
	tokens := strings.Fields(query)
	if len(tokens) == 0 {
		return nil, nil
	}
	if mode == TextMatchExact || mode == TextMatchNormalizeCase {
		if query != strings.Join(tokens, " ") {
			return nil, fmt.Errorf("query whitespace does not match exact OCR token separators")
		}
	}
	if mode == TextMatchNormalizeCase || mode == TextMatchNormalize {
		for i := range tokens {
			tokens[i] = strings.Map(unicode.ToLower, tokens[i])
		}
	}
	return tokens, nil
}

func normalizeOCRWord(text string, mode TextMatchMode) string {
	if mode == TextMatchNormalizeCase || mode == TextMatchNormalize {
		return strings.Map(unicode.ToLower, text)
	}
	return text
}

func mapOCRRect(rect image.Rectangle, capture VerifiedTemplateCapture) (image.Rectangle, error) {
	if rect.Empty() || !rect.In(image.Rect(0, 0, capture.Image.Bounds().Dx(), capture.Image.Bounds().Dy())) || capture.PixelScale <= 0 {
		return image.Rectangle{}, fmt.Errorf("%w: OCR pixel box is outside the verified capture", ErrOCRGeometry)
	}
	scale := capture.PixelScale
	minX, minY := rect.Min.X/scale, rect.Min.Y/scale
	maxX, maxY := (rect.Max.X+scale-1)/scale, (rect.Max.Y+scale-1)/scale
	mapped := image.Rect(capture.DesktopRect.Min.X+minX, capture.DesktopRect.Min.Y+minY, capture.DesktopRect.Min.X+maxX, capture.DesktopRect.Min.Y+maxY)
	if mapped.Empty() || !mapped.In(capture.DesktopRect) {
		return image.Rectangle{}, fmt.Errorf("%w: mapped OCR box exceeds verified desktop region", ErrOCRGeometry)
	}
	return mapped, nil
}
