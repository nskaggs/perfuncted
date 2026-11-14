package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
)

type cancelAfterSuccessfulScreenRead struct {
	cancel context.CancelFunc
	calls  int
}

func (s *cancelAfterSuccessfulScreenRead) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.calls++
	s.cancel()
	return image.NewRGBA(image.Rect(0, 0, 3, 2)), nil
}
func (*cancelAfterSuccessfulScreenRead) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (*cancelAfterSuccessfulScreenRead) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (*cancelAfterSuccessfulScreenRead) Close() error { return nil }

// Captures are not authorized simply because a backend returns a complete
// image. Cancellation during the capture callback must prevent every facade
// entrypoint from exposing those pixels or publishing a PNG.
func TestScreenBundleRejectsSuccessfulCaptureCanceledBeforeReturn(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*ScreenBundle, context.Context, string) (image.Image, error)
	}{
		{"grab", func(b *ScreenBundle, ctx context.Context, _ string) (image.Image, error) {
			return b.Grab(ctx, image.Rectangle{})
		}},
		{"region", func(b *ScreenBundle, ctx context.Context, _ string) (image.Image, error) {
			return b.GrabRegion(ctx, image.Rect(0, 0, 3, 2))
		}},
		{"all pixels", func(b *ScreenBundle, ctx context.Context, _ string) (image.Image, error) { return b.GetAllPixels(ctx) }},
		{"capture file", func(b *ScreenBundle, ctx context.Context, path string) (image.Image, error) {
			return nil, b.CaptureRegion(ctx, image.Rect(0, 0, 3, 2), path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &cancelAfterSuccessfulScreenRead{cancel: cancel}
			session := NewSessionForTesting(source, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			path := filepath.Join(t.TempDir(), "screenshot.png")
			img, err := tc.run(session.Screen, ctx, path)
			if img != nil || !errors.Is(err, context.Canceled) || source.calls != 1 {
				t.Fatalf("canceled backend returned image=%v error=%v calls=%d", img, err, source.calls)
			}
			if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("canceled capture published screenshot: %v", statErr)
			}
		})
	}
}
