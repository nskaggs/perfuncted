package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"
)

type cancelWhenDoneContext struct {
	done   <-chan struct{}
	cancel context.CancelFunc
	once   sync.Once
}

func (c *cancelWhenDoneContext) Done() <-chan struct{} {
	c.once.Do(c.cancel)
	return c.done
}

func (c *cancelWhenDoneContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func (c *cancelWhenDoneContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *cancelWhenDoneContext) Value(key any) any { return nil }

type cancelOnGrabScreenshotter struct {
	img    image.Image
	cancel context.CancelFunc
	grabs  int
}

func (s *cancelOnGrabScreenshotter) cancelOnce() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *cancelOnGrabScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs++
	s.cancelOnce()
	return s.img, nil
}

func (s *cancelOnGrabScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	s.grabs++
	s.cancelOnce()
	return PixelHash(s.img, nil), nil
}

func (s *cancelOnGrabScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	s.grabs++
	s.cancelOnce()
	return PixelHash(s.img, nil), nil
}

func TestWaitFor_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{R: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitFor(ctx, sc, image.Rect(0, 0, 4, 4), want, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitFor error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitFor returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForChange_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{G: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), 0, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForChange error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitForChange returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForChange_CanceledContextBeforeGrabReturnsInitial(t *testing.T) {
	img := solidRGBA(color.RGBA{G: 255, A: 255})
	initial := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sc := &cancelOnGrabScreenshotter{img: img}

	got, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), initial, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForChange error = %v, want context.Canceled", err)
	}
	if got != initial {
		t.Fatalf("WaitForChange returned %08x, want initial hash %08x", got, initial)
	}
	if sc.grabs != 0 {
		t.Fatalf("WaitForChange grabbed %d frames after context cancellation, want 0", sc.grabs)
	}
}

func TestWaitForNoChange_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{B: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForNoChangeFrom(ctx, sc, image.Rect(0, 0, 4, 4), want, 1, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForNoChange error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitForNoChange returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForNoChange_CanceledContextBeforeGrabReturnsInitial(t *testing.T) {
	img := solidRGBA(color.RGBA{B: 255, A: 255})
	initial := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		poll time.Duration
	}{
		{name: "fixed poll", poll: 10 * time.Millisecond},
		{name: "adaptive poll", poll: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &cancelOnGrabScreenshotter{img: img}

			got, err := WaitForNoChangeFrom(ctx, sc, image.Rect(0, 0, 4, 4), initial, 2, tt.poll, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("WaitForNoChange error = %v, want context.Canceled", err)
			}
			if got != initial {
				t.Fatalf("WaitForNoChange returned %08x, want initial hash %08x", got, initial)
			}
			if sc.grabs != 0 {
				t.Fatalf("WaitForNoChange grabbed %d frames after context cancellation, want 0", sc.grabs)
			}
		})
	}
}

func TestScanFor_CanceledContextDuringPollReturnsContextError(t *testing.T) {
	img := solidRGBA(color.RGBA{R: 1, G: 2, B: 3, A: 255})
	base, cancel := context.WithCancel(context.Background())
	ctx := &cancelWhenDoneContext{done: base.Done(), cancel: cancel}

	_, err := ScanFor(
		ctx,
		&solidScreenshotter{},
		[]image.Rectangle{image.Rect(0, 0, 4, 4)},
		[]uint32{PixelHash(img, nil) + 1},
		time.Hour,
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanFor error = %v, want context.Canceled", err)
	}
}

func TestWaitForFn_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{R: 200, G: 100, B: 50, A: 255})
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForFn(ctx, sc, image.Rect(0, 0, 4, 4), func(context.Context, image.Image) bool {
		return true
	}, 10*time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForFn error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("WaitForFn returned image on cancellation, want nil")
	}
}
