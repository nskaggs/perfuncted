package perfuncted

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type syntheticMalformedScreenshotter struct{ frame image.Image }

func (s syntheticMalformedScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return s.frame, nil
}
func (s syntheticMalformedScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (s syntheticMalformedScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (s syntheticMalformedScreenshotter) Close() error { return nil }

func TestScreenBundleNeverPublishesMalformedCaptureOrOverwritesExistingPNG(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"RGBA truncated", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"NRGBA truncated", &image.NRGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"Gray truncated", &image.Gray{Rect: image.Rect(0, 0, 2, 2), Stride: 2, Pix: make([]byte, 3)}},
		{"Paletted truncated", &image.Paletted{Rect: image.Rect(0, 0, 2, 2), Stride: 2, Pix: make([]byte, 3)}},
		{"RGBA64 truncated", &image.RGBA64{Rect: image.Rect(0, 0, 2, 2), Stride: 16, Pix: make([]byte, 25)}},
		{"YCbCr truncated", &image.YCbCr{
			Rect: image.Rect(0, 0, 2, 2), YStride: 2, CStride: 1,
			Y: make([]byte, 3), Cb: make([]byte, 1), Cr: make([]byte, 1),
			SubsampleRatio: image.YCbCrSubsampleRatio420,
		}},
		{"NYCbCrA truncated", &image.NYCbCrA{
			YCbCr: image.YCbCr{
				Rect: image.Rect(0, 0, 2, 2), YStride: 2, CStride: 1,
				Y: make([]byte, 3), Cb: make([]byte, 1), Cr: make([]byte, 1),
				SubsampleRatio: image.YCbCrSubsampleRatio420,
			},
			A: make([]byte, 4), AStride: 2,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: tc.img}, nil, nil, nil, nil)
			defer session.Close()
			ctx := context.Background()
			if _, err := session.Screen.Grab(ctx, image.Rect(0, 0, 2, 2)); err == nil || !strings.Contains(err.Error(), "invalid capture") {
				t.Fatalf("malformed provider image exposed: %v", err)
			}
			target := filepath.Join(t.TempDir(), "existing.png")
			if err := os.WriteFile(target, []byte("preserve original"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := session.Screen.CaptureRegion(ctx, image.Rect(0, 0, 2, 2), target); err == nil || !strings.Contains(err.Error(), "invalid capture") {
				t.Fatalf("malformed provider capture published: %v", err)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "preserve original" {
				t.Fatalf("malformed capture overwrote existing file: %v %q", err, data)
			}
			matches, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".perfuncted-capture-*"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("temporary capture leaked: %v %v", matches, err)
			}
		})
	}
}

func TestScreenBundlePreservesValidOddOriginYCbCrSubimage(t *testing.T) {
	parent := image.NewYCbCr(image.Rect(10, 20, 13, 23), image.YCbCrSubsampleRatio420)
	for i := range parent.Y {
		parent.Y[i] = byte(60 + i)
	}
	for i := range parent.Cb {
		parent.Cb[i] = byte(110 + i)
		parent.Cr[i] = byte(140 + i)
	}
	frame := parent.SubImage(image.Rect(11, 21, 13, 23))
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	defer session.Close()
	ctx := context.Background()
	if got, err := session.Screen.Grab(ctx, frame.Bounds()); err != nil || got != frame {
		t.Fatalf("valid odd-origin YCbCr subimage rejected or rewritten: %v %T", err, got)
	}
	expected := color.RGBAModel.Convert(frame.At(12, 22)).(color.RGBA)
	target := filepath.Join(t.TempDir(), "capture.png")
	if err := session.Screen.CaptureRegion(ctx, frame.Bounds(), target); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		t.Fatalf("valid YCbCr PNG capture undecodable: %v", err)
	}
	if got := color.RGBAModel.Convert(decoded.At(1, 1)).(color.RGBA); got != expected {
		t.Fatalf("odd-origin YCbCr pixel changed: got %+v want %+v", got, expected)
	}
}

// A subimage's Pix ends after its final visible row. It need not contain
// trailing stride padding, and its nonzero bounds are legitimate metadata.
func TestScreenBundlePreservesValidPaddedSubimageAndEncodesExpectedPixels(t *testing.T) {
	frame := &image.RGBA{Rect: image.Rect(10, 20, 12, 22), Stride: 12, Pix: []byte{
		1, 2, 3, 255, 5, 6, 7, 255, 0xc1, 0xc2, 0xc3, 0xc4,
		9, 10, 11, 255, 13, 14, 15, 255,
	}}
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	defer session.Close()
	ctx := context.Background()
	got, err := session.Screen.Grab(ctx, image.Rect(10, 20, 12, 22))
	if err != nil || got != frame {
		t.Fatalf("valid subimage rejected or rewritten: %v %T", err, got)
	}
	target := filepath.Join(t.TempDir(), "capture.png")
	if err := session.Screen.CaptureRegion(ctx, image.Rect(10, 20, 12, 22), target); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, err := png.Decode(file)
	if err != nil {
		t.Fatalf("valid PNG capture undecodable: %v", err)
	}
	if b := decoded.Bounds(); b.Dx() != 2 || b.Dy() != 2 {
		t.Fatalf("invalid encoded dimensions %v", b)
	}
	if got := color.RGBAModel.Convert(decoded.At(1, 1)).(color.RGBA); got != (color.RGBA{R: 13, G: 14, B: 15, A: 255}) {
		t.Fatalf("last visible pixel mutated: %+v", got)
	}
}
