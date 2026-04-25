package perfuncted_test

import (
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

func TestBundleSmoke(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 1024, Height: 768}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{}
	pf := pftest.New(sc, inp, mgr, nil)

	t.Run("Input", func(t *testing.T) {
		if err := pf.Input.ModifierDown("ctrl"); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.ModifierUp("ctrl"); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.TypeWithDelay("hello", 0); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.MouseClick(10, 20, 1); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.DoubleClick(10, 20); err != nil {
			t.Fatal(err)
		}
		if err := pf.Input.DragAndDrop(0, 0, 100, 100); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Screen", func(t *testing.T) {
		if _, err := pf.Screen.GetPixel(5, 5); err != nil {
			t.Fatal(err)
		}
		if _, err := pf.Screen.GetMultiplePixels([]image.Point{{1, 1}}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := pf.Screen.PixelToScreen(image.Rect(0, 0, 1, 1)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Window", func(t *testing.T) {
		_, _ = pf.Window.GetGeometry("Firefox")
		_, _ = pf.Window.GetProcess("Firefox")
		_ = pf.Window.Resize("Firefox", 800, 600)
		_ = pf.Window.Minimize("Firefox")
		_ = pf.Window.Maximize("Firefox")
		_ = pf.Window.Restore("Firefox")
	})
}
