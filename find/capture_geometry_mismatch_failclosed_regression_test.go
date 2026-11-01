package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

// A remote screenshot provider may clip a requested desktop rectangle to a
// smaller buffer. Hashing that partial buffer against the caller's full region
// can satisfy a watch for the wrong pixels, including on compact union scans.
func TestFindRejectsPartialCapturedRegionGeometryWithoutFalseMatches(t *testing.T) {
	partial := image.NewRGBA(image.Rect(0, 0, 1, 1))
	partial.SetRGBA(0, 0, color.RGBA{R: 42, G: 100, B: 110, A: 255})
	hash := PixelHash(partial, nil)
	capture := malformedCaptureBackend{frame: partial}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"direct region hash", func() error {
			_, err := GrabHash(context.Background(), capture, image.Rect(10, 10, 12, 12), nil)
			return err
		}},
		{"compact scan", func() error {
			_, err := ScanFor(context.Background(), capture, []image.Rectangle{image.Rect(10, 10, 11, 11), image.Rect(11, 10, 12, 11)}, []uint32{hash, hash}, 0, nil)
			return err
		}},
		{"sparse scan", func() error {
			_, err := ScanFor(context.Background(), capture, []image.Rectangle{image.Rect(10, 10, 12, 12), image.Rect(100, 100, 102, 102)}, []uint32{hash, hash}, 0, nil)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("partial capture accepted as full region match: %v", err)
			}
		})
	}
}
