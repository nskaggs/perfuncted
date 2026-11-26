package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/pftest"
	"github.com/nskaggs/perfuncted/v2/screen"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"
)

func TestScreenWatchChanges(t *testing.T) {
	region := image.Rect(10, 20, 30, 40)
	bounds := image.Rect(5, 7, 25, 27)
	initial := image.NewRGBA(bounds)
	changed := image.NewRGBA(bounds)
	changed.Pix[0] = 1
	resized := image.NewRGBA(image.Rect(3, 9, 43, 49))
	scaled := image.NewRGBA(resized.Bounds())
	scaled.Pix[3*scaled.Stride+5*4] = 1
	format := image.NewNRGBA(resized.Bounds())
	overflow := image.NewNRGBA(resized.Bounds())
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			if (x+y)%2 == 0 {
				overflow.Pix[y*overflow.Stride+x*4] = 1
			}
		}
	}
	cases := []struct {
		name   string
		frames []image.Image
		want   int
		fail   bool
	}{
		{"changes", []image.Image{initial, initial, changed, changed, resized, scaled, format, overflow}, 6, false},
		{"mapping", []image.Image{image.NewRGBA(image.Rect(0, 0, 20, 10))}, 0, true},
		{"layout", []image.Image{image.NewGray(bounds)}, 0, true},
		{"late-invalid", []image.Image{initial, image.NewGray(bounds)}, 1, true},
		{"malformed", []image.Image{&image.RGBA{Rect: bounds, Stride: 80}}, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n := 0
			sc := &pftest.Screenshotter{GrabFunc: func(ctx context.Context, r image.Rectangle) (image.Image, error) {
				if r != region {
					t.Fatalf("capture region %v", r)
				}
				if n == len(tc.frames) {
					cancel()
					return nil, ctx.Err()
				}
				img := tc.frames[n]
				n++
				return img, nil
			}}
			out, stderr, code := captureRunIOContext(ctx, t, []string{"screen", "watch", "--changes", "--output", "json", "--rect", "10,20,30,40", "--poll", "1ms"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(sc, nil, nil, nil), nil }
			})
			if (code != 0) != tc.fail {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out, stderr)
			}
			dec := json.NewDecoder(strings.NewReader(out))
			for i := 0; i < tc.want; i++ {
				var f watchChangeFrame
				if err := dec.Decode(&f); err != nil {
					t.Fatal(err)
				}
				if f.FrameID != uint64(i+1) || f.Timestamp.IsZero() || f.ScreenRect != region {
					t.Fatalf("frame %+v", f)
				}
				wantVersion := uint64(1)
				if i >= 2 {
					wantVersion = 2
				}
				if f.GeometryVersion != wantVersion {
					t.Fatalf("geometry version: %+v", f)
				}
				if i == 1 {
					if f.FullFrame || f.BaseFrameID == nil || *f.BaseFrameID != 1 || len(f.DirtyRects) != 1 || f.DirtyRects[0] != image.Rect(10, 20, 11, 21) {
						t.Fatalf("delta %+v", f)
					}
				} else if i == 3 {
					if f.FullFrame || f.BaseFrameID == nil || *f.BaseFrameID != 3 || len(f.DirtyRects) != 1 || f.DirtyRects[0] != image.Rect(12, 21, 13, 22) {
						t.Fatalf("scaled delta %+v", f)
					}
				} else if !f.FullFrame || f.BaseFrameID != nil || len(f.DirtyRects) != 1 || f.DirtyRects[0] != region {
					t.Fatalf("full %+v", f)
				}
			}
			// Cobra may write usage on failure; successful streams must be pure NDJSON.
			if !tc.fail {
				var extra any
				if err := dec.Decode(&extra); err != io.EOF {
					t.Fatalf("extra output: %v %v", extra, err)
				}
			}
		})
	}
}

func TestScreenWatchLegacyHashCompatibility(t *testing.T) {
	for _, mode := range []string{"plain", "json"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			n := 0
			img := image.NewRGBA(image.Rect(0, 0, 2, 2))
			sc := &pftest.Screenshotter{GrabFunc: func(ctx context.Context, _ image.Rectangle) (image.Image, error) {
				n++
				if n == 3 {
					cancel()
					return nil, ctx.Err()
				}
				return img, nil
			}}
			args := []string{"screen", "watch", "--rect", "0,0,2,2", "--poll", "1ms"}
			if mode == "json" {
				args = append(args, "--output", "json")
			}
			out, stderr, code := captureRunIOContext(ctx, t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(sc, nil, nil, nil), nil }
			})
			if code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr)
			}
			if mode == "plain" {
				if len(out) != len("00:00:00.000  0xecbb4b55  (initial)\n") || out[12:] != "  0xecbb4b55  (initial)\n" {
					t.Fatalf("legacy %q", out)
				}
			} else {
				var m map[string]any
				if err := json.Unmarshal([]byte(out), &m); err != nil || len(m) != 3 || m["event"] != "initial" || m["hash"] != "0xecbb4b55" || m["timestamp"] == nil {
					t.Fatalf("legacy %q: %v", out, err)
				}
			}
		})
	}
}

func TestScreenWatchChangesNegativePoll(t *testing.T) {
	opens := 0
	_, stderr, code := captureRunIOContext(context.Background(), t, []string{"screen", "watch", "--changes", "--output", "json", "--poll=-1ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opens++
			t.Fatal("opened session for negative poll")
			return nil, nil
		}
	})
	if code == 0 || opens != 0 || !strings.Contains(stderr, "non-negative") {
		t.Fatalf("code=%d opens=%d stderr=%q", code, opens, stderr)
	}
}

// Measures the real PNG/base64/JSON encoder for sparse crops of a synthetic
// full-HD capture. Unchanged frames are suppressed by the watch path.
func BenchmarkWatchPixelFrameFullHD(b *testing.B) {
	b.Run("Sparse", func(b *testing.B) {
		bounds := image.Rect(0, 0, 1920, 1080)
		img := image.NewRGBA(bounds)
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(i/4), byte(i/7680), 64, 255
		}
		capture := screen.Capture{Image: img, ScreenRect: bounds}
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
		err := watchScreenChanges(ctx, &out, region, time.Nanosecond, func(context.Context, image.Rectangle) (screen.Capture, error) {
			n++
			if n == 3 {
				cancel()
				return screen.Capture{}, ctx.Err()
			}
			img := a
			if n == 2 {
				img = b
			}
			return screen.Capture{Image: img, ScreenRect: region}, nil
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
	c := screen.Capture{Image: image.NewNRGBA(r), ScreenRect: r}
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
			err := watchScreenChanges(ctx, &out, region, time.Nanosecond, func(context.Context, image.Rectangle) (screen.Capture, error) {
				if n == len(frames) {
					cancel()
					return screen.Capture{}, ctx.Err()
				}
				img := frames[n]
				n++
				return screen.Capture{Image: img, ScreenRect: region}, nil
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
