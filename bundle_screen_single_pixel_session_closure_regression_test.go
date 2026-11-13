package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

type closeDuringSinglePixelRead struct {
	after  func()
	called bool
}

func (i *closeDuringSinglePixelRead) ColorModel() color.Model { return color.RGBAModel }
func (i *closeDuringSinglePixelRead) Bounds() image.Rectangle { return image.Rect(0, 0, 2, 2) }
func (i *closeDuringSinglePixelRead) At(x, y int) color.Color {
	if !i.called {
		i.called = true
		i.after()
	}
	return color.RGBA{R: 10, G: 20, B: 30, A: 255}
}

// The image adapter may synchronously close the owning session while reading
// the requested pixel. A closed session cannot publish the sampled color.
func TestScreenGetPixelRejectsResultAfterSessionClosedDuringRead(t *testing.T) {
	img := &closeDuringSinglePixelRead{}
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: img}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	img.after = func() { _ = session.Close() }
	pixel, err := session.Screen.GetPixel(context.Background(), 0, 0)
	if !img.called || !errors.Is(err, ErrSessionClosed) || pixel != (color.RGBA{}) {
		t.Fatalf("closed session exposed pixel %+v err=%v img called=%v", pixel, err, img.called)
	}
}
