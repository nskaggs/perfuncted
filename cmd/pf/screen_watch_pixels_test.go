package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nskaggs/perfuncted/v2"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"
)

// Measures the real PNG/base64/JSON encoder for sparse crops of a synthetic
// full-HD capture. Unchanged frames are suppressed by the watch path.
func BenchmarkWatchPixelFrameFullHD(b *testing.B) {
	b.Run("Sparse", func(b *testing.B) {
		bounds := image.Rect(0, 0, 1920, 1080)
		img := image.NewRGBA(bounds)
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(i/4), byte(i/7680), 64, 255
		}
		capture := perfuncted.Capture{Image: img, ScreenRect: bounds}
		crops := []image.Rectangle{image.Rect(100, 100, 132, 132), image.Rect(160, 140, 192, 172), image.Rect(1800, 20, 1864, 36)}
		frame := watchChangeFrame{GeometryVersion: 1}
		ctx := context.Background()
		var wire benchmarkWireCounter
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := writeWatchPixelFrame(ctx, &wire, frame, capture, crops, watchPixelFrameLimit); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(wire)/float64(b.N), "wire-B/frame")
	})
}

type benchmarkWireCounter int64

func (c *benchmarkWireCounter) Write(p []byte) (int, error) {
	*c += benchmarkWireCounter(len(p))
	return len(p), nil
}

func TestScreenWatchPixelPayloads(t *testing.T) {
	bounds := image.Rect(5, 7, 9, 11)
	region := image.Rect(10, 20, 12, 22)
	a := image.NewNRGBA(bounds)
	b := image.NewNRGBA(bounds)
	for y := 7; y < 11; y++ {
		for x := 5; x < 9; x++ {
			c := color.NRGBA{uint8(x), uint8(y), 42, 255}
			a.SetNRGBA(x, y, c)
			b.SetNRGBA(x, y, c)
		}
	}
	b.SetNRGBA(6, 8, color.NRGBA{100, 20, 30, 255})
	b.SetNRGBA(8, 10, color.NRGBA{80, 90, 10, 255})
	for _, opt := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var out bytes.Buffer
		n := 0
		err := watchScreenChanges(ctx, &out, region, time.Nanosecond, func(context.Context, image.Rectangle) (perfuncted.Capture, error) {
			n++
			if n == 3 {
				cancel()
				return perfuncted.Capture{}, ctx.Err()
			}
			img := a
			if n == 2 {
				img = b
			}
			return perfuncted.Capture{Image: img, ScreenRect: region}, nil
		}, opt)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if !opt && strings.Contains(out.String(), "\"pixels\"") {
			t.Fatal("metadata disclosed pixels")
		}
		dec := json.NewDecoder(&out)
		for i, src := range []*image.NRGBA{a, b} {
			var f watchChangeFrame
			if err := dec.Decode(&f); err != nil {
				t.Fatal(err)
			}
			if !opt {
				continue
			}
			want := []image.Rectangle{bounds}
			if i == 1 {
				want = []image.Rectangle{image.Rect(6, 8, 7, 9), image.Rect(8, 10, 9, 11)}
			}
			if len(f.Pixels) != len(want) {
				t.Fatalf("payload count %d", len(f.Pixels))
			}
			for j, p := range f.Pixels {
				r := want[j]
				if p.Encoding != "png+base64" || p.ImageBounds != r || p.Width != r.Dx() || p.Height != r.Dy() || p.PixelScale != 2 || p.GeometryVersion != f.GeometryVersion {
					t.Fatalf("provenance %+v", p)
				}
				img, err := png.Decode(bytes.NewReader(p.Data))
				if err != nil {
					t.Fatal(err)
				}
				if img.Bounds().Size() != r.Size() {
					t.Fatal("crop dimensions")
				}
				for y := 0; y < r.Dy(); y++ {
					for x := 0; x < r.Dx(); x++ {
						if color.NRGBAModel.Convert(img.At(x, y)) != src.NRGBAAt(r.Min.X+x, r.Min.Y+y) {
							t.Fatal("wrong crop pixel")
						}
					}
				}
			}
		}
	}
}

func TestScreenWatchPixelLimit(t *testing.T) {
	r := image.Rect(0, 0, 8, 8)
	c := perfuncted.Capture{Image: image.NewNRGBA(r), ScreenRect: r}
	f := watchChangeFrame{ImageBounds: r, ScreenRect: r, GeometryVersion: 1}
	for _, limit := range []int{32, 200} {
		var out bytes.Buffer
		if err := writeWatchPixelFrame(context.Background(), &out, f, c, []image.Rectangle{r}, limit); err == nil || out.Len() != 0 {
			t.Fatalf("limit=%d err=%v bytes=%d", limit, err, out.Len())
		}
	}
}

func TestScreenWatchPixelOptions(t *testing.T) {
	for _, extra := range [][]string{nil, {"--changes"}, {"--output", "json"}} {
		args := append([]string{"screen", "watch", "--include-pixels"}, extra...)
		_, stderr, code := captureRunIOContext(context.Background(), t, args, func(*cliConfig) sessionOpener {
			return func(context.Context) (*perfuncted.Session, error) { t.Fatal("opened session"); return nil, nil }
		})
		if code == 0 || !strings.Contains(stderr, "--include-pixels requires") {
			t.Fatalf("code=%d stderr=%s", code, stderr)
		}
	}
}

func TestScreenWatchFullCoverageResync(t *testing.T) {
	bounds := image.Rect(5, 7, 9, 11)
	region := image.Rect(10, 20, 12, 22)
	a := image.NewNRGBA(bounds)
	b := image.NewNRGBA(bounds)
	c := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			a.SetNRGBA(x, y, color.NRGBA{A: 255})
			b.SetNRGBA(x, y, color.NRGBA{R: 100, A: 255})
			c.SetNRGBA(x, y, color.NRGBA{R: 100, A: 255})
		}
	}
	c.SetNRGBA(6, 8, color.NRGBA{R: 200, A: 255})
	for _, mode := range []struct {
		name   string
		pixels bool
	}{{"metadata", false}, {"pixels", true}} {
		t.Run(mode.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out bytes.Buffer
			frames := []*image.NRGBA{a, b, b, c}
			n := 0
			err := watchScreenChanges(ctx, &out, region, time.Nanosecond, func(context.Context, image.Rectangle) (perfuncted.Capture, error) {
				if n == len(frames) {
					cancel()
					return perfuncted.Capture{}, ctx.Err()
				}
				img := frames[n]
				n++
				return perfuncted.Capture{Image: img, ScreenRect: region}, nil
			}, mode.pixels)
			if err != nil {
				t.Fatal(err)
			}
			if !mode.pixels && strings.Contains(out.String(), "\"pixels\"") {
				t.Fatal("metadata disclosed pixels")
			}
			dec := json.NewDecoder(&out)
			for i, src := range []*image.NRGBA{a, b, c} {
				var f watchChangeFrame
				if err := dec.Decode(&f); err != nil {
					t.Fatal(err)
				}
				if f.FrameID != uint64(i+1) || f.GeometryVersion != 1 || f.ScreenRect != region || f.ImageBounds != bounds {
					t.Fatalf("provenance %+v", f)
				}
				crop, dirty := bounds, region
				if i < 2 {
					if !f.FullFrame || f.BaseFrameID != nil {
						t.Fatalf("expected resync %+v", f)
					}
				} else {
					if f.FullFrame || f.BaseFrameID == nil || *f.BaseFrameID != 2 {
						t.Fatalf("expected delta from new base %+v", f)
					}
					crop = image.Rect(6, 8, 7, 9)
					dirty = image.Rect(10, 20, 11, 21)
				}
				if len(f.DirtyRects) != 1 || f.DirtyRects[0] != dirty {
					t.Fatalf("dirty rectangles %+v", f)
				}
				if !mode.pixels {
					continue
				}
				if len(f.Pixels) != 1 {
					t.Fatalf("payload count %d", len(f.Pixels))
				}
				p := f.Pixels[0]
				if p.ImageBounds != crop || p.Width != crop.Dx() || p.Height != crop.Dy() || p.GeometryVersion != 1 {
					t.Fatalf("payload provenance %+v", p)
				}
				img, err := png.Decode(bytes.NewReader(p.Data))
				if err != nil {
					t.Fatal(err)
				}
				if img.Bounds().Size() != crop.Size() {
					t.Fatal("payload size")
				}
				for y := 0; y < crop.Dy(); y++ {
					for x := 0; x < crop.Dx(); x++ {
						if color.NRGBAModel.Convert(img.At(x, y)) != src.NRGBAAt(crop.Min.X+x, crop.Min.Y+y) {
							t.Fatal("wrong payload pixel")
						}
					}
				}
			}
			if dec.More() {
				t.Fatal("unexpected extra frame")
			}
		})
	}
}
