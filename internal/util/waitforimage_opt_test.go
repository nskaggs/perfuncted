package util_test

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/util"
)

type countedScreenshotter struct {
	img   image.Image
	grabs int
}

func (s *countedScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs++
	return s.img, nil
}

func (s *countedScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	return 0, nil
}

func (s *countedScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	return 0, nil
}

func (s *countedScreenshotter) Close() error { return nil }

type countedResolutionScreenshotter struct {
	countedScreenshotter
}

func (s *countedResolutionScreenshotter) Resolution() (int, int, error) {
	b := s.img.Bounds()
	return b.Dx(), b.Dy(), nil
}

func waitForImageFixture() (image.Image, image.Image) {
	big := image.NewRGBA(image.Rect(0, 0, 4, 4))
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	ref.SetRGBA(0, 0, color.RGBA{R: 10, A: 255})
	ref.SetRGBA(1, 0, color.RGBA{G: 20, A: 255})
	ref.SetRGBA(0, 1, color.RGBA{B: 30, A: 255})
	ref.SetRGBA(1, 1, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			big.SetRGBA(1+x, 1+y, ref.RGBAAt(x, y))
		}
	}
	return big, ref
}

func TestWaitForImageUsesResolutionWhenAvailable(t *testing.T) {
	big, ref := waitForImageFixture()
	sc := &countedResolutionScreenshotter{countedScreenshotter{img: big}}

	res, err := util.WaitForImage(sc, ref, "exact", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForImage: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].Rect != image.Rect(1, 1, 3, 3) {
		t.Fatalf("unexpected rect: %v", res[0].Rect)
	}
	if sc.grabs != 1 {
		t.Fatalf("Grab calls = %d, want 1", sc.grabs)
	}
}

func TestWaitForImageFallsBackWithoutResolution(t *testing.T) {
	big, ref := waitForImageFixture()
	sc := &countedScreenshotter{img: big}

	res, err := util.WaitForImage(sc, ref, "exact", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForImage: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].Rect != image.Rect(1, 1, 3, 3) {
		t.Fatalf("unexpected rect: %v", res[0].Rect)
	}
	if sc.grabs != 2 {
		t.Fatalf("Grab calls = %d, want 2", sc.grabs)
	}
}

func BenchmarkWaitForImageWithResolution(b *testing.B) {
	big, ref := waitForImageBenchmarkFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc := &benchmarkResolutionScreenshotter{benchmarkScreenshotter{img: big}}
		if _, err := util.WaitForImage(sc, ref, "exact", time.Second); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWaitForImageWithoutResolution(b *testing.B) {
	big, ref := waitForImageBenchmarkFixture()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc := &benchmarkScreenshotter{img: big}
		if _, err := util.WaitForImage(sc, ref, "exact", time.Second); err != nil {
			b.Fatal(err)
		}
	}
}

type benchmarkScreenshotter struct {
	img image.Image
}

func (s *benchmarkScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return cloneImage(s.img), nil
}

func (s *benchmarkScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	return 0, nil
}

func (s *benchmarkScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	return 0, nil
}

func (s *benchmarkScreenshotter) Close() error { return nil }

type benchmarkResolutionScreenshotter struct {
	benchmarkScreenshotter
}

func (s *benchmarkResolutionScreenshotter) Resolution() (int, int, error) {
	b := s.img.Bounds()
	return b.Dx(), b.Dy(), nil
}

func waitForImageBenchmarkFixture() (image.Image, image.Image) {
	big := image.NewRGBA(image.Rect(0, 0, 640, 480))
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	ref.SetRGBA(0, 0, color.RGBA{R: 10, A: 255})
	ref.SetRGBA(1, 0, color.RGBA{G: 20, A: 255})
	ref.SetRGBA(0, 1, color.RGBA{B: 30, A: 255})
	ref.SetRGBA(1, 1, color.RGBA{R: 40, G: 50, B: 60, A: 255})
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			big.SetRGBA(1+x, 1+y, ref.RGBAAt(x, y))
		}
	}
	return big, ref
}

func cloneImage(img image.Image) image.Image {
	bounds := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(out, out.Bounds(), img, bounds.Min, draw.Src)
	return out
}
