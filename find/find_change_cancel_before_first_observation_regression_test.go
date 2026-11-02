package find

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

// A backend may report cancellation before publishing even its first frame.
// In that case the only known hash is the caller's initial hash: a zero
// result incorrectly claims a pixel observation that never happened.
type noFrameCanceledScreenshotter struct {
	cancel context.CancelFunc
	reads  int
}

func (s *noFrameCanceledScreenshotter) Grab(ctx context.Context, _ image.Rectangle) (image.Image, error) {
	s.reads++
	s.cancel()
	return nil, ctx.Err()
}
func (s *noFrameCanceledScreenshotter) GrabFullHash(context.Context) (uint32, error) {
	panic("not supported")
}
func (s *noFrameCanceledScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	panic("not supported")
}
func (s *noFrameCanceledScreenshotter) Close() error { return nil }

func TestWaitForChangeDoesNotReplaceKnownHashWhenFirstGrabCancelsWithoutFrame(t *testing.T) {
	initial := uint32(0x1234feed)
	for _, pollInterval := range []time.Duration{5 * time.Millisecond, 0} {
		t.Run(pollInterval.String(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &noFrameCanceledScreenshotter{cancel: cancel}
			got, err := WaitForChange(ctx, source, image.Rect(0, 0, 4, 4), initial, pollInterval, nil)
			if !errors.Is(err, context.Canceled) || got != initial {
				t.Fatalf("missing first sample was reported as hash %08x with error %v, want last known %08x", got, err, initial)
			}
			if source.reads != 1 {
				t.Fatalf("expected exactly one failed attempt, got %d", source.reads)
			}
		})
	}
}
