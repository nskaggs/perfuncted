package screen

import (
	"image"
	"image/color"
	"reflect"
	"testing"
)

func TestDirtyRects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bounds image.Rectangle
		points []image.Point
		limit  int
		want   []image.Rectangle
		resync bool
	}{
		{"unchanged", image.Rect(0, 0, 5, 5), nil, 1, nil, false},
		{"single", image.Rect(0, 0, 5, 5), []image.Point{{2, 3}}, 1, []image.Rectangle{image.Rect(2, 3, 3, 4)}, false},
		{"adjacent", image.Rect(-4, 7, 1, 12), []image.Point{{-3, 8}, {-2, 8}, {-3, 9}, {-2, 9}}, 1, []image.Rectangle{image.Rect(-3, 8, -1, 10)}, false},
		{"different spans", image.Rect(0, 0, 5, 5), []image.Point{{0, 0}, {1, 0}, {1, 1}, {4, 0}}, 3, []image.Rectangle{image.Rect(0, 0, 2, 1), image.Rect(4, 0, 5, 1), image.Rect(1, 1, 2, 2)}, false},
		{"overflow", image.Rect(0, 0, 5, 5), []image.Point{{0, 0}, {2, 0}}, 1, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b := image.NewRGBA(tc.bounds), image.NewRGBA(tc.bounds)
			for _, p := range tc.points {
				b.SetRGBA(p.X, p.Y, color.RGBA{R: 255, A: 255})
			}
			got, resync, err := dirtyRects(a, b, tc.limit)
			if err != nil || resync != tc.resync || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v, %v; want %v, %v", got, resync, err, tc.want, tc.resync)
			}
		})
	}
}

func TestDirtyRectsGeometry(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for _, b := range []image.Image{image.NewRGBA(image.Rect(0, 0, 3, 2)), image.NewRGBA(image.Rect(1, 1, 3, 3)), image.NewNRGBA(a.Rect)} {
		got, resync, err := dirtyRects(a, b, 1)
		if err != nil || !resync || got != nil {
			t.Fatalf("got %v, %v, %v", got, resync, err)
		}
	}
}

func TestDirtyRectsMalformed(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	max := int(^uint(0) >> 1)
	for _, b := range []image.Image{
		nil, (*image.RGBA)(nil), image.NewRGBA(image.Rectangle{}),
		&image.RGBA{Rect: image.Rectangle{Min: image.Pt(2, 2), Max: image.Pt(1, 1)}},
		&image.RGBA{Rect: a.Rect, Stride: 7, Pix: make([]byte, 16)},
		&image.RGBA{Rect: a.Rect, Stride: 8, Pix: make([]byte, 15)},
		&image.RGBA{Rect: a.Rect, Stride: max, Pix: make([]byte, 16)},
		&image.RGBA{Rect: image.Rectangle{Min: image.Pt(-max, 0), Max: image.Pt(max, 1)}, Stride: 8, Pix: make([]byte, 16)},
		image.NewGray(a.Rect),
	} {
		for _, pair := range [][2]image.Image{{a, b}, {b, a}} {
			got, resync, err := dirtyRects(pair[0], pair[1], 1)
			if err == nil || resync || got != nil {
				t.Fatalf("accepted %T: %v, %v, %v", b, got, resync, err)
			}
		}
	}
	if _, _, err := dirtyRects(a, a, 0); err == nil {
		t.Fatal("accepted zero limit")
	}
}

func TestDirtyRectsPaddedNRGBA(t *testing.T) {
	a := image.NewNRGBA(image.Rect(-5, -5, 5, 5)).SubImage(image.Rect(-2, -2, 2, 2)).(*image.NRGBA)
	b := image.NewNRGBA(a.Rect)
	b.SetNRGBA(-1, 0, color.NRGBA{B: 17})
	got, resync, err := dirtyRects(a, b, 1)
	if err != nil || resync || !reflect.DeepEqual(got, []image.Rectangle{image.Rect(-1, 0, 0, 1)}) {
		t.Fatalf("got %v, %v, %v", got, resync, err)
	}
}
