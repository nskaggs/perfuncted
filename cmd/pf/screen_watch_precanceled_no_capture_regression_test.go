package main

import (
	"context"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// A watch canceled after session setup but before its first sample must exit
// cleanly without capturing pixels or writing a misleading initial sample.
func TestScreenWatchCanceledBeforeInitialSampleDoesNotCapture(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	samples := 0
	opens := 0
	capture := &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) {
		samples++
		t.Fatal("canceled watch tried to sample private pixels")
		return nil, context.Canceled
	}}
	out, errOut, code := captureRunIOContext(ctx, t, []string{"screen", "watch", "--rect", "0,0,2,2", "--poll", "1ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opens++
			return pftest.New(capture, nil, nil, nil), nil
		}
	})
	if code != 0 || opens != 1 || samples != 0 || out != "" {
		t.Fatalf("canceled watch code=%d opens=%d samples=%d stdout=%q stderr=%q", code, opens, samples, out, errOut)
	}
}
