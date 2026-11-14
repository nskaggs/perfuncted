package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

type closeDuringHashReadImage struct {
	onRead func()
	reads  int
}

func (*closeDuringHashReadImage) Bounds() image.Rectangle { return image.Rect(0, 0, 2, 2) }
func (*closeDuringHashReadImage) ColorModel() color.Model { return color.RGBAModel }
func (i *closeDuringHashReadImage) At(int, int) color.Color {
	i.reads++
	if i.reads == 1 {
		i.onRead()
	}
	return color.RGBA{R: 25, G: 34, B: 55, A: 255}
}

// A backend may return a lazy image whose hash computation runs after an
// owner closure. Neither full-screen nor region hashes can be published once
// the owner revokes access, even if the hash implementation completes.
func TestScreenHashesDoNotPublishAfterClosureDuringPixelHash(t *testing.T) {
	for _, tc := range []struct {
		name string
		hash func(*ScreenBundle) (uint32, error)
	}{
		{"full", func(b *ScreenBundle) (uint32, error) { return b.GrabFullHash(context.Background()) }},
		{"region", func(b *ScreenBundle) (uint32, error) {
			return b.GrabRegionHash(context.Background(), image.Rect(0, 0, 2, 2))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := &closeDuringHashReadImage{}
			session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			img.onRead = func() {
				if err := session.Close(); err != nil {
					t.Errorf("close session: %v", err)
				}
			}
			value, err := tc.hash(session.Screen)
			if value != 0 || !errors.Is(err, ErrSessionClosed) || img.reads == 0 {
				t.Fatalf("closed session published hash %08x, error=%v, pixel reads=%d", value, err, img.reads)
			}
		})
	}
}
