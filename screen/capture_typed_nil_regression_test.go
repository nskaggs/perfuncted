package screen

import (
	"image"
	"testing"
)

// A nil concrete image stored in an interface must remain a failed capture,
// not a successful frame or a nil pointer panic during Bounds().
func TestRequireCapturedImageRejectsTypedNilAndEmptyFrames(t *testing.T) {
	var rgba *image.RGBA
	var nrgba *image.NRGBA
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"typed nil RGBA", rgba},
		{"typed nil NRGBA", nrgba},
		{"empty RGBA", image.NewRGBA(image.Rectangle{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := requireCapturedImage("test", tc.img)
			if err == nil || got != nil {
				t.Fatalf("accepted invalid image: image=%v error=%v", got, err)
			}
		})
	}
}
