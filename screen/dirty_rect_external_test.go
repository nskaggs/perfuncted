package screen_test

import (
	"github.com/nskaggs/perfuncted/v2/screen"
	"image"
	"image/color"
	"reflect"
	"testing"
)

// Separate full-HD buffers model two captures, not aliasing the same frame.
func BenchmarkDirtyRectsFullHD(b *testing.B) {
	for _, sparse := range []bool{false, true} {
		name := "NoChange"
		if sparse {
			name = "Sparse"
		}
		b.Run(name, func(b *testing.B) {
			bounds := image.Rect(0, 0, 1920, 1080)
			before, after := image.NewRGBA(bounds), image.NewRGBA(bounds)
			for i := 0; i < len(before.Pix); i += 4 {
				before.Pix[i], before.Pix[i+1], before.Pix[i+2], before.Pix[i+3] = byte(i/4), byte(i/7680), 64, 255
			}
			copy(after.Pix, before.Pix)
			// Cursor-sized old/new locations and a small status indicator.
			crops := []image.Rectangle{image.Rect(100, 100, 132, 132), image.Rect(160, 140, 192, 172), image.Rect(1800, 20, 1864, 36)}
			if sparse {
				for _, r := range crops {
					for y := r.Min.Y; y < r.Max.Y; y++ {
						for x := r.Min.X; x < r.Max.X; x++ {
							after.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
						}
					}
				}
			}
			want := 0
			if sparse {
				want = len(crops)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, resync, err := screen.DirtyRects(before, after, 64)
				if err != nil || resync || len(got) != want {
					b.Fatalf("rectangles=%v resync=%v err=%v", got, resync, err)
				}
			}
		})
	}
}

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
			got, resync, err := screen.DirtyRects(a, b, tc.limit)
			if err != nil || resync != tc.resync || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v, %v; want %v, %v", got, resync, err, tc.want, tc.resync)
			}
		})
	}
}

func TestDirtyRectsGeometry(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for _, b := range []image.Image{image.NewRGBA(image.Rect(0, 0, 3, 2)), image.NewRGBA(image.Rect(1, 1, 3, 3)), image.NewNRGBA(a.Rect)} {
		got, resync, err := screen.DirtyRects(a, b, 1)
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
			got, resync, err := screen.DirtyRects(pair[0], pair[1], 1)
			if err == nil || resync || got != nil {
				t.Fatalf("accepted %T: %v, %v, %v", b, got, resync, err)
			}
		}
	}
	if _, _, err := screen.DirtyRects(a, a, 0); err == nil {
		t.Fatal("accepted zero limit")
	}
}

func TestDirtyRectsPaddedNRGBA(t *testing.T) {
	a, ok := image.NewNRGBA(image.Rect(-5, -5, 5, 5)).SubImage(image.Rect(-2, -2, 2, 2)).(*image.NRGBA)
	if !ok {
		t.Fatal("unexpected image type")
	}
	b := image.NewNRGBA(a.Rect)
	b.SetNRGBA(-1, 0, color.NRGBA{B: 17})
	got, resync, err := screen.DirtyRects(a, b, 1)
	if err != nil || resync || !reflect.DeepEqual(got, []image.Rectangle{image.Rect(-1, 0, 0, 1)}) {
		t.Fatalf("got %v, %v, %v", got, resync, err)
	}
}

// Exhaust every 3x3 change pattern, using padded storage, a nonzero
// origin, and hidden color bytes. Every changed pixel must be included
// exactly once; unchanged pixels must never be emitted.
func TestReviewDirtyRectExhaustivePatterns(t *testing.T) {
	bounds := image.Rect(-2, 5, 1, 8)
	for mask := 0; mask < 1<<9; mask++ {
		a, ok := image.NewNRGBA(image.Rect(-4, 3, 4, 10)).SubImage(bounds).(*image.NRGBA)
		if !ok {
			t.Fatal("unexpected image type")
		}
		b := image.NewNRGBA(bounds)
		for i := 0; i < 9; i++ {
			if mask&(1<<i) != 0 {
				b.Pix[(i/3)*b.Stride+(i%3)*4] = 123
			}
		}
		rects, full, err := screen.DirtyRects(a, b, 9)
		if err != nil || full {
			t.Fatalf("mask=%09b: rects=%v full=%v err=%v", mask, rects, full, err)
		}
		var reconstructed int
		for _, r := range rects {
			if r.Empty() || !r.In(bounds) {
				t.Fatalf("mask=%09b invalid rectangle %v", mask, r)
			}
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					bit := 1 << ((y-bounds.Min.Y)*3 + x - bounds.Min.X)
					if reconstructed&bit != 0 {
						t.Fatalf("mask=%09b duplicate pixel (%d,%d)", mask, x, y)
					}
					reconstructed |= bit
				}
			}
		}
		if reconstructed != mask {
			t.Fatalf("mask=%09b reconstructed=%09b rects=%v", mask, reconstructed, rects)
		}
		again, full, err := screen.DirtyRects(a, b, 9)
		if err != nil || full || !reflect.DeepEqual(again, rects) {
			t.Fatalf("mask=%09b nondeterministic", mask)
		}
		for limit := 1; limit <= 9; limit++ {
			got, resync, err := screen.DirtyRects(a, b, limit)
			if err != nil || resync != (len(rects) > limit) || (resync && got != nil) || (!resync && !reflect.DeepEqual(got, rects)) {
				t.Fatalf("mask=%09b limit=%d got=%v resync=%v err=%v", mask, limit, got, resync, err)
			}
		}
	}
}
