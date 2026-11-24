package find

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nskaggs/perfuncted/internal/util"
)

const (
	maxOCRPNGBytes = 32 << 20
	maxOCRTSVBytes = 4 << 20
	maxOCRStderr   = 16 << 10
	maxOCRLangList = 64 << 10
)

var tesseractLanguagePattern = regexp.MustCompile(`^[A-Za-z0-9_]+(?:\+[A-Za-z0-9_]+)*$`)
var errOCROutputLimit = errors.New("OCR output limit exceeded")

// TesseractProvider runs an optional local Tesseract executable without a
// shell, temporary files, network access, or unbounded output. Construct it
// explicitly with NewTesseractProvider; the default Perfuncted runtime never
// enables OCR implicitly.
type TesseractProvider struct {
	executable string
	language   string
}

// NewTesseractProvider resolves the executable before callers capture a
// screen. An empty executable selects "tesseract" from PATH. Language is a
// Tesseract language identifier such as "eng" or "eng+deu".
func NewTesseractProvider(executable, language string) (*TesseractProvider, error) {
	if executable == "" {
		executable = "tesseract"
	}
	if language == "" || !tesseractLanguagePattern.MatchString(language) {
		return nil, fmt.Errorf("find: invalid Tesseract language %q: %w", language, ErrOCRProviderUnavailable)
	}
	path, err := exec.LookPath(executable)
	if err != nil {
		return nil, fmt.Errorf("find: resolve Tesseract executable: %w: %v", ErrOCRProviderUnavailable, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stdout := limitedBuffer{limit: maxOCRLangList}
	stderr := limitedBuffer{limit: maxOCRStderr}
	cmd := exec.CommandContext(ctx, path, "--list-langs")
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("find: query Tesseract languages: %w", ErrOCRProviderUnavailable)
	}
	available := map[string]bool{}
	for _, languageLine := range strings.Split(stdout.String(), "\n") {
		languageLine = strings.TrimSpace(languageLine)
		if languageLine != "" && !strings.HasPrefix(languageLine, "List of available languages") {
			available[languageLine] = true
		}
	}
	for _, requestedLanguage := range strings.Split(language, "+") {
		if !available[requestedLanguage] {
			return nil, fmt.Errorf("find: Tesseract language %q is not installed: %w", requestedLanguage, ErrOCRProviderUnavailable)
		}
	}
	return &TesseractProvider{executable: path, language: language}, nil
}

// Name identifies the local OCR engine.
func (p *TesseractProvider) Name() string { return "tesseract" }

// Language reports the language configuration selected for this provider.
func (p *TesseractProvider) Language() string { return p.language }

// Recognize sends a bounded PNG over stdin and parses Tesseract TSV output.
// Tesseract's relative word boxes are returned in image pixel coordinates.
func (p *TesseractProvider) Recognize(ctx context.Context, img image.Image) ([]OCRWord, error) {
	if p == nil || p.executable == "" || p.language == "" {
		return nil, fmt.Errorf("%w: Tesseract provider was not constructed", ErrOCRProviderUnavailable)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if util.IsNil(img) || img.Bounds().Empty() {
		return nil, fmt.Errorf("find: Tesseract input image is empty: %w", ErrInvalidFrame)
	}
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || width > 8192 || height > 8192 || width > maxOCRImagePixels/height {
		return nil, fmt.Errorf("find: Tesseract input dimensions %dx%d exceed limits: %w", width, height, ErrInvalidFrame)
	}
	var input limitedBuffer
	input.limit = maxOCRPNGBytes
	if err := png.Encode(&input, img); err != nil {
		if input.overflow {
			return nil, fmt.Errorf("find: Tesseract input PNG exceeded %d bytes: %w", maxOCRPNGBytes, ErrInvalidFrame)
		}
		return nil, fmt.Errorf("find: encode Tesseract input PNG: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stdout, stderr, overflow, runErr := runBoundedOCRCommand(ctx, p.executable, []string{"stdin", "stdout", "--psm", "6", "-l", p.language, "tsv"}, input.Bytes())
	if runErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if errors.Is(runErr, errOCROutputLimit) || overflow {
			return nil, fmt.Errorf("find: Tesseract output exceeded configured limit: %w", ErrOCRRecognition)
		}
		if missingTesseractLanguage(string(stderr)) {
			return nil, fmt.Errorf("find: Tesseract language %q became unavailable: %w", p.language, ErrOCRProviderUnavailable)
		}
		return nil, fmt.Errorf("find: Tesseract process failed: %w", runErr)
	}
	if overflow {
		return nil, fmt.Errorf("find: Tesseract output exceeded configured limit: %w", ErrOCRRecognition)
	}
	words, err := parseTesseractTSV(stdout, width, height)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return words, nil
}

func runBoundedOCRCommand(ctx context.Context, executable string, args []string, input []byte) (stdout, stderr []byte, overflow bool, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	stdoutBuffer := limitedBuffer{limit: maxOCRTSVBytes}
	stderrBuffer := limitedBuffer{limit: maxOCRStderr}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Stdout, cmd.Stderr = &stdoutBuffer, &stderrBuffer
	err = cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = ctxErr
	}
	return bytes.Clone(stdoutBuffer.Bytes()), bytes.Clone(stderrBuffer.Bytes()), stdoutBuffer.overflow || stderrBuffer.overflow, err
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	remaining := b.limit - b.Len()
	if len(data) > remaining {
		if remaining > 0 {
			_, _ = b.Buffer.Write(data[:remaining])
		}
		b.overflow = true
		return remaining, errOCROutputLimit
	}
	return b.Buffer.Write(data)
}

func missingTesseractLanguage(stderr string) bool {
	return strings.Contains(stderr, "Failed loading language") || strings.Contains(stderr, "Error opening data file")
}

func parseTesseractTSV(data []byte, width, height int) ([]OCRWord, error) {
	if len(data) == 0 || len(data) > maxOCRTSVBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("find: malformed or oversized Tesseract TSV: %w", ErrOCRRecognition)
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = '\t'
	reader.FieldsPerRecord = 12
	reader.ReuseRecord = true
	header, err := reader.Read()
	if err != nil || strings.Join(header, "\t") != "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext" {
		return nil, fmt.Errorf("find: malformed Tesseract TSV header: %w", ErrOCRRecognition)
	}
	words := make([]OCRWord, 0, 64)
	for {
		row, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("find: malformed Tesseract TSV row: %w", ErrOCRRecognition)
		}
		if len(words) >= maxOCRWords {
			return nil, fmt.Errorf("find: Tesseract returned too many words: %w", ErrOCRRecognition)
		}
		level, parseErr := strconv.Atoi(row[0])
		if parseErr != nil || level < 1 || level > 5 {
			return nil, fmt.Errorf("find: invalid Tesseract TSV level: %w", ErrOCRRecognition)
		}
		if level != 5 {
			continue
		}
		text := row[11]
		if strings.TrimSpace(text) == "" {
			continue
		}
		if len(text) > maxOCRWordBytes || !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || len(strings.Fields(text)) != 1 {
			return nil, fmt.Errorf("find: invalid Tesseract word text: %w", ErrOCRRecognition)
		}
		ids := [5]int{}
		for i := range ids {
			ids[i], parseErr = strconv.Atoi(row[i+1])
			if parseErr != nil || ids[i] < 0 {
				return nil, fmt.Errorf("find: invalid Tesseract TSV identity: %w", ErrOCRRecognition)
			}
		}
		coords := [4]int{}
		for i := range coords {
			coords[i], parseErr = strconv.Atoi(row[i+6])
			if parseErr != nil || coords[i] < 0 {
				return nil, fmt.Errorf("find: invalid Tesseract TSV bounds: %w", ErrOCRRecognition)
			}
		}
		if coords[2] <= 0 || coords[3] <= 0 || coords[0] > width-coords[2] || coords[1] > height-coords[3] {
			return nil, fmt.Errorf("find: Tesseract word box exceeds input image: %w", ErrOCRRecognition)
		}
		confidenceValue, parseErr := strconv.ParseFloat(row[10], 64)
		if parseErr != nil || math.IsNaN(confidenceValue) || math.IsInf(confidenceValue, 0) || confidenceValue < 0 || confidenceValue > 100 {
			return nil, fmt.Errorf("find: invalid Tesseract confidence: %w", ErrOCRRecognition)
		}
		confidence := confidenceValue
		words = append(words, OCRWord{
			Text: text, PixelRect: image.Rect(coords[0], coords[1], coords[0]+coords[2], coords[1]+coords[3]), Confidence: &confidence,
			Page: ids[0], Block: ids[1], Paragraph: ids[2], Line: ids[3], Word: ids[4],
		})
	}
	return words, nil
}
