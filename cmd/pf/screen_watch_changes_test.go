package main

import (
	"context"
	"encoding/json"
	"image"
	"io"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/pftest"
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
