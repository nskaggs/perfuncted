package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
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
