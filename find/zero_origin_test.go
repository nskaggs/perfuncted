package find

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"
)

type zeroOriginScreenshotter struct {
	frame image.Image
}

func (z *zeroOriginScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	target := rect
	if target.Empty() {
		target = z.frame.Bounds()
	}
	target = target.Intersect(z.frame.Bounds())
	if target.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 0, 0)), nil
	}
	out := image.NewRGBA(image.Rect(0, 0, target.Dx(), target.Dy()))
	draw.Draw(out, out.Bounds(), z.frame, target.Min, draw.Src)
	return out, nil
}

func (z *zeroOriginScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	return PixelHash(z.frame, nil), nil
}

func (z *zeroOriginScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := z.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return PixelHash(img, nil), nil
}

type fullFrameScreenshotter struct {
	frame image.Image
}

func (s fullFrameScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return s.frame, nil
}

func TestLocateExactZeroOriginCapture(t *testing.T) {
	full := image.NewRGBA(image.Rect(10, 20, 30, 40))
	needle := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for y := range 3 {
		for x := range 3 {
			c := color.RGBA{R: 10, G: uint8(20 + x), B: uint8(30 + y), A: 255}
			full.SetRGBA(14+x, 23+y, c)
			needle.SetRGBA(x, y, c)
		}
	}

	sc := &zeroOriginScreenshotter{frame: full}
	found, err := LocateExact(context.Background(), sc, image.Rect(10, 20, 30, 40), needle)
	if err != nil {
		t.Fatal(err)
	}
	want := image.Rect(14, 23, 17, 26)
	if found != want {
		t.Fatalf("found = %v, want %v", found, want)
	}
}

func TestScanForZeroOriginCapture(t *testing.T) {
	full := image.NewRGBA(image.Rect(10, 20, 30, 40))
	region1 := image.Rect(12, 22, 14, 24)
	region2 := image.Rect(16, 24, 18, 26)
	for y := region1.Min.Y; y < region1.Max.Y; y++ {
		for x := region1.Min.X; x < region1.Max.X; x++ {
			full.SetRGBA(x, y, color.RGBA{R: 1, G: 2, B: 3, A: 255})
		}
	}
	for y := region2.Min.Y; y < region2.Max.Y; y++ {
		for x := region2.Min.X; x < region2.Max.X; x++ {
			full.SetRGBA(x, y, color.RGBA{R: 9, G: 8, B: 7, A: 255})
		}
	}

	ref1 := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			ref1.SetRGBA(x, y, color.RGBA{R: 1, G: 2, B: 3, A: 255})
		}
	}
	want1 := PixelHash(ref1, nil)
	ref2 := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			ref2.SetRGBA(x, y, color.RGBA{R: 9, G: 8, B: 7, A: 255})
		}
	}
	want2 := PixelHash(ref2, nil)

	sc := &zeroOriginScreenshotter{frame: full}
	res, err := ScanFor(context.Background(), sc, []image.Rectangle{region1, region2}, []uint32{want1, want2}, 1*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Hash != want1 {
		t.Fatalf("res.Hash = %08x, want %08x", res.Hash, want1)
	}
	if res.Rect != region1 {
		t.Fatalf("res.Rect = %v, want %v", res.Rect, region1)
	}
}

func TestFindColorZeroOriginCapture(t *testing.T) {
	full := image.NewRGBA(image.Rect(10, 20, 30, 40))
	target := color.RGBA{R: 23, G: 45, B: 67, A: 255}
	full.SetRGBA(13, 24, target)

	sc := &zeroOriginScreenshotter{frame: full}
	p, err := FindColor(context.Background(), sc, image.Rect(10, 20, 30, 40), target, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := image.Pt(13, 24)
	if p != want {
		t.Fatalf("p = %v, want %v", p, want)
	}
}

func TestFindColorFullScreenCapturePreservesImageOrigin(t *testing.T) {
	bounds := image.Rect(10, 20, 30, 40)
	want := image.Pt(13, 24)
	gray := color.Gray{Y: 128}

	tests := []struct {
		name   string
		frame  image.Image
		target color.RGBA
	}{
		{
			name: "rgba",
			frame: func() image.Image {
				img := image.NewRGBA(bounds)
				img.SetRGBA(want.X, want.Y, color.RGBA{R: 23, G: 45, B: 67, A: 255})
				return img
			}(),
			target: color.RGBA{R: 23, G: 45, B: 67, A: 255},
		},
		{
			name: "generic image",
			frame: func() image.Image {
				img := image.NewGray(bounds)
				img.SetGray(want.X, want.Y, gray)
				return img
			}(),
			target: color.RGBA{R: gray.Y, G: gray.Y, B: gray.Y, A: 255},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindColor(context.Background(), fullFrameScreenshotter{frame: tt.frame}, image.Rectangle{}, tt.target, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("FindColor point = %v, want %v", got, want)
			}
		})
	}
}

func TestFindColorFullFrameImageHonorsRequestedRegion(t *testing.T) {
	frame := image.NewRGBA(image.Rect(10, 20, 30, 40))
	target := color.RGBA{R: 23, G: 45, B: 67, A: 255}
	frame.SetRGBA(10, 20, target)
	frame.SetRGBA(15, 26, target)

	got, err := FindColor(
		context.Background(),
		fullFrameScreenshotter{frame: frame},
		image.Rect(13, 24, 18, 29),
		target,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := image.Pt(15, 26); got != want {
		t.Fatalf("FindColor point = %v, want %v", got, want)
	}
}
