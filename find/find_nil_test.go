package find

import (
	"context"
	"image"
	"testing"
	"time"
)

func TestNilScreenshotterRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sc   Screenshotter
	}{
		{name: "nil interface", sc: nil},
		{name: "typed nil", sc: Screenshotter((*solidScreenshotter)(nil))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := GrabHash(context.Background(), tc.sc, image.Rect(0, 0, 1, 1), nil); err == nil {
				t.Fatal("GrabHash succeeded unexpectedly")
			}
			if _, err := WaitForFn(context.Background(), tc.sc, image.Rect(0, 0, 1, 1), func(context.Context, image.Image) bool { return true }, time.Millisecond); err == nil {
				t.Fatal("WaitForFn succeeded unexpectedly")
			}
		})
	}
}
