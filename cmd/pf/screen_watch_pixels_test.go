package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
)

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
