package main

import (
	"bytes"
	"context"
	"image"
	"math"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

func rawScreenshotCommand(t *testing.T, frame image.Image) ([]byte, error) {
	t.Helper()
	command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
		return pftest.New(&pftest.Screenshotter{Frames: []image.Image{frame}}, nil, nil, nil), nil
	})
	var output bytes.Buffer
	command.SilenceUsage = true
	command.SilenceErrors = true
	command.SetOut(&output)
	command.SetArgs([]string{"get-all-pixels"})
	err := command.ExecuteContext(context.Background())
	return bytes.Clone(output.Bytes()), err
}

// Row padding is not pixel data; it can contain uninitialized or sensitive
// backing-buffer bytes. The CLI promises exactly width*height*4 RGBA bytes.
func TestRawScreenshotOutputOmitsProviderRowPaddingAndTrailingBytes(t *testing.T) {
	frame := &image.RGBA{Rect: image.Rect(20, 30, 22, 32), Stride: 12, Pix: []byte{
		1, 2, 3, 4, 5, 6, 7, 8, 0xf1, 0xf2, 0xf3, 0xf4,
		9, 10, 11, 12, 13, 14, 15, 16, 0xe1, 0xe2, 0xe3, 0xe4,
	}}
	got, err := rawScreenshotCommand(t, frame)
	if err != nil {
		t.Fatalf("raw capture: %v", err)
	}
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if !bytes.Equal(got, want) {
		t.Fatalf("raw stream disclosed nonpixel bytes or wrong stride: got %x want %x", got, want)
	}
}

func TestRawScreenshotRejectsMalformedProviderBuffersBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame *image.RGBA
	}{
		{"truncated row", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"invalid stride", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 4, Pix: make([]byte, 8)}},
		{"negative stride", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: -8, Pix: make([]byte, 16)}},
		{"huge width overflow", &image.RGBA{Rect: image.Rect(0, 0, math.MaxInt, 2), Stride: 8, Pix: make([]byte, 16)}},
		{"inverted bounds", &image.RGBA{Rect: image.Rectangle{Min: image.Pt(10, 0), Max: image.Pt(1, 2)}, Stride: 8, Pix: make([]byte, 16)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rawScreenshotCommand(t, tc.frame)
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("malformed provider buffer accepted: %v", err)
			}
			if len(got) > 0 {
				t.Fatalf("malformed provider buffer emitted %d bytes", len(got))
			}
		})
	}
}

// A valid subimage may omit trailing row padding. The final row only needs
// enough backing bytes for its actual pixels, not another complete stride.
func TestRawScreenshotAcceptsMinimalFinalRowOfPaddedSubimage(t *testing.T) {
	frame := &image.RGBA{Rect: image.Rect(9, 12, 11, 14), Stride: 12, Pix: []byte{
		1, 2, 3, 4, 5, 6, 7, 8, 0xfa, 0xfb, 0xfc, 0xfd,
		9, 10, 11, 12, 13, 14, 15, 16,
	}}
	got, err := rawScreenshotCommand(t, frame)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if !bytes.Equal(got, want) {
		t.Fatalf("valid padded subimage bytes %x, want %x", got, want)
	}
}
