package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTesseractProviderRecognizesRenderedLocalText(t *testing.T) {
	provider, err := NewTesseractProvider("", "eng")
	if errors.Is(err, ErrOCRProviderUnavailable) {
		t.Skip("optional local Tesseract is not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	const scale = 12
	glyphs := map[rune][]string{
		'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
		'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
		'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
		'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	}
	img := image.NewGray(image.Rect(0, 0, 420, 140))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	xOrigin, yOrigin := 12, 24
	for i, letter := range []rune("HELLO") {
		for y, row := range glyphs[letter] {
			for x, bit := range row {
				if bit != '1' {
					continue
				}
				for sy := 0; sy < scale; sy++ {
					for sx := 0; sx < scale; sx++ {
						img.SetGray(xOrigin+i*7*scale+x*scale+sx, yOrigin+y*scale+sy, color.Gray{Y: 0})
					}
				}
			}
		}
	}
	result, err := RecognizeTextInImage(context.Background(), img, "HELLO", TextOptions{Recognizer: provider, MatchMode: TextMatchExact})
	if err != nil {
		t.Fatal(err)
	}
	if result.CoordinateSpace != "image-pixels" || len(result.Matches) != 1 || result.Matches[0].Text != "HELLO" || result.Matches[0].PixelRect.Empty() || result.Matches[0].Confidence == nil {
		t.Fatalf("Tesseract image query result=%+v", result)
	}
}

const validTesseractHeader = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"

func TestParseTesseractTSVWordsAndConfidence(t *testing.T) {
	data := validTesseractHeader +
		"5\t1\t2\t3\t4\t1\t8\t9\t10\t11\t87.5\tContinue\n" +
		"5\t1\t2\t3\t4\t2\t20\t9\t15\t11\t92\tnow\n"
	words, err := parseTesseractTSV([]byte(data), 40, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(words) != 2 || words[0].Text != "Continue" || words[0].PixelRect != image.Rect(8, 9, 18, 20) || words[0].Page != 1 || words[0].Block != 2 || words[0].Paragraph != 3 || words[0].Line != 4 || words[0].Word != 1 || words[0].Confidence == nil || *words[0].Confidence != 87.5 {
		t.Fatalf("first word=%+v", words)
	}
}

func TestParseTesseractTSVRejectsMalformedUntrustedRows(t *testing.T) {
	cases := map[string]string{
		"header":              "wrong\theader\n",
		"columns":             validTesseractHeader + "5\t1\t2\n",
		"negative coordinate": validTesseractHeader + "5\t1\t2\t3\t4\t1\t-1\t0\t2\t2\t90\tbad\n",
		"outside image":       validTesseractHeader + "5\t1\t2\t3\t4\t1\t9\t9\t2\t2\t90\tbad\n",
		"invalid confidence":  validTesseractHeader + "5\t1\t2\t3\t4\t1\t0\t0\t2\t2\t101\tbad\n",
		"whitespace word":     validTesseractHeader + "5\t1\t2\t3\t4\t1\t0\t0\t2\t2\t90\ttwo words\n",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTesseractTSV([]byte(data), 10, 10); !errors.Is(err, ErrOCRRecognition) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := parseTesseractTSV([]byte(validTesseractHeader+"5\t1\t2\t3\t4\t1\t0\t0\t2\t2\tNaN\tbad\n"), 10, 10); !errors.Is(err, ErrOCRRecognition) {
		t.Fatalf("NaN confidence error=%v", err)
	}
}

func TestNewTesseractProviderRequiresSafeLanguageAndInstalledExecutable(t *testing.T) {
	if _, err := NewTesseractProvider(filepath.Join(t.TempDir(), "missing"), "eng"); !errors.Is(err, ErrOCRProviderUnavailable) {
		t.Fatalf("missing executable error=%v", err)
	}
	if _, err := NewTesseractProvider("tesseract", "eng;touch-owned-file"); !errors.Is(err, ErrOCRProviderUnavailable) {
		t.Fatalf("unsafe language error=%v", err)
	}
}

func TestTesseractDiagnosticsAndOutputLimitAreTyped(t *testing.T) {
	if !missingTesseractLanguage("Error opening data file /private/path/eng.traineddata") || missingTesseractLanguage("generic recognition failure") {
		t.Fatal("Tesseract language diagnostics were not classified")
	}
	buffer := limitedBuffer{limit: 3}
	n, err := buffer.Write([]byte("four"))
	if n != 3 || !errors.Is(err, errOCROutputLimit) || !buffer.overflow || buffer.String() != "fou" {
		t.Fatalf("write n=%d err=%v overflow=%v output=%q", n, err, buffer.overflow, buffer.String())
	}
}

func TestBoundedOCRCommandKillsCanceledProcess(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep executable is not available for process-cancellation test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _, _, err = runBoundedOCRCommand(ctx, sleep, []string{"30"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("command error=%v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("canceled child took %s to exit", time.Since(started))
	}
}
