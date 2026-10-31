package main

import (
	"context"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

func TestScreenWatchCancellationDuringCaptureExitsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc := &pftest.Screenshotter{GrabFunc: func(ctx context.Context, _ image.Rectangle) (image.Image, error) {
		cancel()
		return nil, ctx.Err()
	}}
	stdout, stderr, code := captureRunIOContext(ctx, t,
		[]string{"screen", "watch", "--rect", "0,0,2,2", "--poll", "1ms", "--output", "json"},
		func(*cliConfig) sessionOpener {
			return func(context.Context) (*perfuncted.Session, error) {
				return pftest.New(sc, nil, nil, nil), nil
			}
		})
	if code != 0 {
		t.Fatalf("canceled screen watch exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
