package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/pftest"
)

func TestSyntheticScreenCaptureCommandsWriteRealImageAndPixels(t *testing.T) {
	pixels := pftest.SolidImage(8, 8, color.RGBA{R: 12, G: 34, B: 56, A: 255})
	for _, tc := range []struct {
		name string
		args []string
		want string
		path bool
	}{
		{"region hash", []string{"screen", "hash", "--rect", "0,0,8,8"}, fmt.Sprintf("%08x", find.PixelHash(pixels, nil)), false},
		{"pixel", []string{"screen", "pixel", "--x", "4", "--y", "3"}, "R=12 G=34 B=56", false},
		{"resolution", []string{"screen", "resolution"}, "8x8", false},
		{"grab", []string{"screen", "grab", "--rect", "0,0,8,8"}, "", true},
		{"grab region", []string{"screen", "grab-region", "--rect", "0,0,8,8"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path {
				tc.args = append(tc.args, "--out", filepath.Join(t.TempDir(), "screen.png"))
			}
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{Frames: []image.Image{pixels}}, nil, nil, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("screenshot command %v failed code=%d out=%q err=%q", tc.args, code, out, stderr)
			}
			if tc.path {
				p := tc.args[len(tc.args)-1]
				f, err := os.Open(p)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				decoded, err := png.Decode(f)
				if err != nil {
					t.Fatalf("capture is not PNG: %v", err)
				}
				if decoded.Bounds().Dx() != 8 || decoded.Bounds().Dy() != 8 {
					t.Fatalf("wrong PNG geometry: %v", decoded.Bounds())
				}
				r, g, b, _ := decoded.At(3, 4).RGBA()
				if r>>8 != 12 || g>>8 != 34 || b>>8 != 56 {
					t.Fatalf("corrupted screenshot pixel: %d %d %d", r>>8, g>>8, b>>8)
				}
			}
		})
	}
}
func TestSyntheticScreenCommandsRejectCaptureFailures(t *testing.T) {
	for _, args := range [][]string{{"screen", "hash"}, {"screen", "pixel"}, {"screen", "grab-region", "--rect", "0,0,8,8", "--out", "-"}, {"screen", "get-all-pixels"}, {"find", "color", "--color", "0c2238", "--rect", "0,0,8,8"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{Err: os.ErrPermission}, nil, nil, nil), nil
				}
			})
			if code == 0 || strings.Contains(out, "0x") || !strings.Contains(stderr, "permission denied") {
				t.Fatalf("failed capture unexpectedly succeeded code=%d out=%q err=%q", code, out, stderr)
			}
		})
	}
}
func TestSyntheticFindCLIHashAndColorMatches(t *testing.T) {
	pixels := pftest.SolidImage(8, 8, color.RGBA{R: 12, G: 34, B: 56, A: 255})
	hash := find.PixelHash(pixels, nil)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"find color", []string{"find", "color", "--color", "0c2238", "--rect", "0,0,8,8"}, "0,0"},
		{"wait hash", []string{"find", "wait-for", "--rect", "0,0,8,8", "--hash", fmt.Sprintf("0x%x", hash), "--poll", "1ms", "--timeout", "150ms"}, fmt.Sprintf("%08x", hash)},
		{"stable hash", []string{"find", "wait-for-no-change", "--rect", "0,0,8,8", "--stable", "2", "--poll", "1ms", "--timeout", "150ms"}, fmt.Sprintf("%08x", hash)},
		{"multi rect scan", []string{"find", "scan-for", "--rects", "0,0,8,8", "--wants", fmt.Sprintf("0x%x", hash), "--poll", "1ms", "--timeout", "150ms"}, "match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{Frames: []image.Image{pixels}}, nil, nil, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("matching command %v code=%d out=%q err=%q want=%q", tc.args, code, out, stderr, tc.want)
			}
		})
	}
}

func TestSyntheticScreenAndFindCommandsObserveActualFrameChanges(t *testing.T) {
	before := pftest.SolidImage(8, 8, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	after := pftest.SolidImage(8, 8, color.RGBA{R: 20, G: 30, B: 40, A: 255})
	initialHash := find.PixelHash(before, nil)
	changedHash := find.PixelHash(after, nil)
	for _, tc := range []struct {
		name   string
		args   []string
		want   string
		images []image.Image
	}{
		{"find wait for change", []string{"find", "wait-for-change", "--rect", "0,0,8,8", "--initial", fmt.Sprintf("0x%x", initialHash), "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{after}},
		{"find change after capture", []string{"find", "wait-for-change", "--rect", "0,0,8,8", "--capture-initial", "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{before, after}},
		{"find visible change", []string{"find", "wait-for-visible-change", "--rect", "0,0,8,8", "--stable", "1", "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{before, after}},
		{"find stable after change", []string{"screen", "wait-for-settle", "--rect", "0,0,8,8", "--stable", "2", "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{before, after}},
		{"nonempty predicate", []string{"screen", "wait-for-fn", "--rect", "0,0,8,8", "--predicate", "non-empty", "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{after}},
		{"multiple pixels plain", []string{"screen", "get-multiple-pixels", "--points", "1,2;3,4"}, "R=20 G=30 B=40 A=255", []image.Image{after}},
		{"multiple pixels JSON", []string{"screen", "get-multiple-pixels", "--points", "1,2;3,4", "--output", "json"}, `"x":1`, []image.Image{after}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{Frames: tc.images}, nil, nil, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("changing frames command %v: code=%d stdout=%q stderr=%q want %q", tc.args, code, out, stderr, tc.want)
			}
		})
	}
}

func TestSyntheticScreenAndFindRejectInvalidInputsBeforeAnyCapture(t *testing.T) {
	cases := [][]string{
		{"find", "wait-for", "--rect", "not-a-rect", "--hash", "0x1234"},
		{"find", "wait-for", "--rect", "0,0,8,8", "--hash", "invalid"},
		{"find", "wait-for-change", "--rect", "bad", "--initial", "0x12"},
		{"find", "wait-for-change", "--rect", "0,0,8,8", "--initial", "invalid"},
		{"find", "wait-for-no-change", "--rect", "bad"},
		{"find", "scan-for", "--rects", "bad", "--wants", "0x1234"},
		{"find", "scan-for", "--rects", "0,0,8,8", "--wants", "nothex"},
		{"find", "scan-for", "--rects", "0,0,8,8;1,1,2,2", "--wants", "0x1234"},
		{"find", "color", "--rect", "0,0,8,8", "--color", "not-hex"},
		{"screen", "wait-for-fn", "--rect", "0,0,8,8", "--predicate", "invalid"},
		{"screen", "wait-for-settle", "--rect", "0,0,8,8", "--stable", "not-a-count"},
		{"screen", "get-multiple-pixels", "--points", "not-coordinates"},
		{"screen", "get-multiple-pixels", "--points", "1,2", "--output", "garbage"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			calls := 0
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) {
						calls++
						return pftest.SolidImage(8, 8, color.RGBA{A: 255}), nil
					}}, nil, nil, nil), nil
				}
			})
			if code == 0 || stderr == "" || strings.Contains(out, "match") {
				t.Fatalf("invalid input %v was accepted: stdout=%q stderr=%q code=%d", args, out, stderr, code)
			}
			// Some invalid output-mode configurations are checked after collecting a
			// valid sample; all other rejected forms must not reach the transport.
			if calls != 0 && !(len(args) > 1 && args[1] == "get-multiple-pixels" && strings.Contains(strings.Join(args, " "), "garbage")) {
				t.Fatalf("invalid argument reached screenshot transport %v: %d grabs", args, calls)
			}
		})
	}
}
