package main

import (
	"bytes"
	"context"
	"image"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// Reject malformed capture buffers before PNG encoding. Encoding untrusted
// image.RGBA can read outside Pix, or emit PNG headers before discovering an
// unusable capture; neither a crash nor a partial image is a valid response.
func TestPNGScreenCaptureRejectsTruncatedProviderBufferBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{
		{"RGBA short last row", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"RGBA overlapping rows", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 4, Pix: make([]byte, 12)}},
		{"NRGBA short last row", &image.NRGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) { return tc.frame, nil }}
			cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(sc, nil, nil, nil), nil })
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"grab-region", "--rect", "0,0,2,2", "--out", "-"})
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("malformed PNG source accepted: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("invalid capture emitted %d PNG bytes before validation", out.Len())
			}
		})
	}
}
