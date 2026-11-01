package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

// NRGBA stores straight-alpha channels, but color.RGBAModel and public
// PixelFound targets are premultiplied RGBA. All supported capture image types
// must agree on whether a translucent pixel matches a requested color.
func TestPixelFoundNRGBAAlphaUsesCanonicalRGBAColorAcrossBackends(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 3))
	source.SetNRGBA(2, 1, color.NRGBA{R: 255, G: 75, B: 15, A: 64})
	target := color.RGBAModel.Convert(source.NRGBAAt(2, 1)).(color.RGBA)
	wrong := color.RGBA{R: 255, G: 75, B: 15, A: 255}
	for _, tc := range []struct {
		name  string
		image image.Image
	}{
		{"native NRGBA", source},
		{"generic image adapter", syntheticImage{source}},
		{"canonical RGBA copy", func() image.Image {
			converted := image.NewRGBA(source.Bounds())
			for y := 0; y < 3; y++ {
				for x := 0; x < 3; x++ {
					converted.Set(x, y, source.At(x, y))
				}
			}
			return converted
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pixel, found := PixelFound(tc.image, tc.image.Bounds(), target, 0)
			if !found || pixel != (image.Pt(2, 1)) {
				t.Fatalf("translucent pixel not matched by canonical RGBA: found=%v pixel=%v target=%v", found, pixel, target)
			}
			if pixel, found := PixelFound(tc.image, tc.image.Bounds(), wrong, 0); found {
				t.Fatalf("straight-alpha color falsely matched premultiplied target: %v", pixel)
			}
			point, err := FindColor(context.Background(), malformedCaptureBackend{frame: tc.image}, tc.image.Bounds(), target, 0)
			if err != nil || point != (image.Pt(2, 1)) {
				t.Fatalf("FindColor translucent result=%v error=%v", point, err)
			}
			_, err = FindColor(context.Background(), malformedCaptureBackend{frame: tc.image}, tc.image.Bounds(), wrong, 0)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("FindColor incorrectly matched straight-alpha bytes: %v", err)
			}
		})
	}
}
