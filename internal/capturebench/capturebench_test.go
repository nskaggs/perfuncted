package capturebench

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"
	"time"
)

type fakeScreenshotter struct {
	grab func(context.Context, image.Rectangle) (image.Image, error)
}

func (f fakeScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return f.grab(ctx, rect)
}

type funcScreenshotter func(context.Context, image.Rectangle) (image.Image, error)

func (f funcScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return f(ctx, rect)
}

type steppingContext struct {
	calls    int
	cancelAt int
}

func (*steppingContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*steppingContext) Done() <-chan struct{}       { return nil }
func (c *steppingContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
func (*steppingContext) Value(any) any { return nil }

func TestSummarize(t *testing.T) {
	low, err := Summarize([]time.Duration{4, 1, 3, 2})
	if err != nil || low.Count != 4 || low.Min != 1 || low.P50 != 2 || low.HasP95 || low.Max != 4 {
		t.Fatalf("low-N distribution=%+v err=%v", low, err)
	}
	largeInput := make([]time.Duration, 100)
	for i := range largeInput {
		largeInput[i] = time.Duration(i + 1)
	}
	large, err := Summarize(largeInput)
	if err != nil || large.P50 != 50 || large.P95 != 95 || !large.HasP95 || large.Max != 100 {
		t.Fatalf("100-sample distribution=%+v err=%v", large, err)
	}
	if largeInput[0] != 1 || largeInput[99] != 100 {
		t.Fatal("Summarize changed caller sample order")
	}
	odd, err := Summarize([]time.Duration{3, 1, 2})
	if err != nil || odd.P50 != 2 {
		t.Fatalf("odd-N distribution=%+v err=%v", odd, err)
	}
	identical, err := Summarize([]time.Duration{5, 5, 5})
	if err != nil || identical.Min != 5 || identical.P50 != 5 || identical.Max != 5 {
		t.Fatalf("identical distribution=%+v err=%v", identical, err)
	}
	units, err := Summarize([]time.Duration{time.Millisecond, time.Nanosecond, time.Microsecond})
	if err != nil || units.P50 != time.Microsecond {
		t.Fatalf("mixed-unit distribution=%+v err=%v", units, err)
	}
	outliers := make([]time.Duration, 100)
	for i := range outliers {
		outliers[i] = time.Millisecond
	}
	outliers[len(outliers)-1] = time.Second
	withOutlier, err := Summarize(outliers)
	if err != nil || withOutlier.P95 != time.Millisecond || withOutlier.Max != time.Second {
		t.Fatalf("outlier distribution=%+v err=%v", withOutlier, err)
	}
	if _, err := Summarize(nil); err == nil {
		t.Fatal("empty samples accepted")
	}
	if _, err := Summarize([]time.Duration{time.Second, -1}); err == nil {
		t.Fatal("negative duration accepted")
	}
}

func TestCollectGrabAndRegionGeometry(t *testing.T) {
	region := image.Rect(3, 4, 11, 12)
	calls := 0
	sc := fakeScreenshotter{grab: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
		calls++
		if requested != region {
			t.Fatalf("Grab got region %v, want %v", requested, region)
		}
		return image.NewRGBA(region), nil
	}}
	durations, bounds, err := Collect(context.Background(), sc, region, 3, time.Second)
	if err != nil || len(durations) != 3 || bounds != region || calls != 3 {
		t.Fatalf("samples=%d bounds=%v calls=%d err=%v", len(durations), bounds, calls, err)
	}
}

func TestCollectRejectsInvalidInputs(t *testing.T) {
	sc := fakeScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
	}}
	var typedNil *fakeScreenshotter
	var nilFunc funcScreenshotter
	for _, tt := range []struct {
		name    string
		ctx     context.Context
		sc      Screenshotter
		count   int
		timeout time.Duration
	}{
		{name: "nil context", sc: sc, count: 1, timeout: time.Second},
		{name: "nil backend", ctx: context.Background(), count: 1, timeout: time.Second},
		{name: "typed nil pointer", ctx: context.Background(), sc: typedNil, count: 1, timeout: time.Second},
		{name: "typed nil function", ctx: context.Background(), sc: nilFunc, count: 1, timeout: time.Second},
		{name: "nonpositive count", ctx: context.Background(), sc: sc, timeout: time.Second},
		{name: "negative count", ctx: context.Background(), sc: sc, count: -1, timeout: time.Second},
		{name: "nonpositive timeout", ctx: context.Background(), sc: sc, count: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Collect(tt.ctx, tt.sc, image.Rectangle{}, tt.count, tt.timeout); err == nil {
				t.Fatal("invalid arguments accepted")
			}
		})
	}
}

func TestCollectCapturesAndPreservesFailureCounts(t *testing.T) {
	t.Run("backend error", func(t *testing.T) {
		want := errors.New("backend failed")
		calls := 0
		sc := fakeScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
			calls++
			if calls == 1 {
				return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
			}
			return nil, want
		}}
		durations, _, err := Collect(context.Background(), sc, image.Rectangle{}, 3, time.Second)
		var collectionErr *CollectionError
		if len(durations) != 1 || !errors.As(err, &collectionErr) || collectionErr.Sample != 2 || collectionErr.Succeeded != 1 || collectionErr.TimedOut || !errors.Is(err, want) {
			t.Fatalf("durations=%d error=%#v", len(durations), err)
		}
		if collectionErr.Error() == "" || collectionErr.Unwrap() != want || strings.Contains(collectionErr.Error(), want.Error()) {
			t.Fatal("collection error did not preserve its cause")
		}
	})
	t.Run("timeout", func(t *testing.T) {
		sc := fakeScreenshotter{grab: func(ctx context.Context, _ image.Rectangle) (image.Image, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		_, _, err := Collect(context.Background(), sc, image.Rectangle{}, 1, time.Millisecond)
		var collectionErr *CollectionError
		if !errors.As(err, &collectionErr) || !collectionErr.TimedOut || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error=%#v", err)
		}
	})
	t.Run("backend returns after per-capture deadline", func(t *testing.T) {
		sc := fakeScreenshotter{grab: func(ctx context.Context, _ image.Rectangle) (image.Image, error) {
			<-ctx.Done()
			return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
		}}
		durations, _, err := Collect(context.Background(), sc, image.Rectangle{}, 1, time.Millisecond)
		var collectionErr *CollectionError
		if len(durations) != 0 || !errors.As(err, &collectionErr) || !collectionErr.TimedOut || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("durations=%v timeout error=%#v", durations, err)
		}
	})
	t.Run("caller canceled before capture", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := Collect(ctx, fakeScreenshotter{}, image.Rectangle{}, 1, time.Second)
		var collectionErr *CollectionError
		if !errors.As(err, &collectionErr) || collectionErr.Sample != 1 || !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled error=%#v", err)
		}
	})
	t.Run("caller deadline before capture", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, _, err := Collect(ctx, fakeScreenshotter{}, image.Rectangle{}, 1, time.Second)
		var collectionErr *CollectionError
		if !errors.As(err, &collectionErr) || !collectionErr.TimedOut || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline error=%#v", err)
		}
	})
	t.Run("caller canceled after response", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		sc := fakeScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
			cancel()
			return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
		}}
		_, _, err := Collect(ctx, sc, image.Rectangle{}, 1, time.Second)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late cancellation error=%v", err)
		}
	})
	t.Run("caller canceled before later sample", func(t *testing.T) {
		ctx := &steppingContext{cancelAt: 4}
		sc := fakeScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
			return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
		}}
		durations, _, err := Collect(ctx, sc, image.Rectangle{}, 2, time.Second)
		var collectionErr *CollectionError
		if len(durations) != 1 || !errors.As(err, &collectionErr) || collectionErr.Sample != 2 || collectionErr.Succeeded != 1 {
			t.Fatalf("durations=%d error=%v", len(durations), err)
		}
	})
	t.Run("nil image", func(t *testing.T) {
		sc := fakeScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) { return nil, nil }}
		_, _, err := Collect(context.Background(), sc, image.Rectangle{}, 1, time.Second)
		if err == nil {
			t.Fatal("nil image accepted")
		}
	})
	t.Run("typed nil image", func(t *testing.T) {
		sc := fakeScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
			var img *image.RGBA
			return img, nil
		}}
		_, _, err := Collect(context.Background(), sc, image.Rectangle{}, 1, time.Second)
		if err == nil {
			t.Fatal("typed nil image accepted")
		}
	})
	t.Run("empty image", func(t *testing.T) {
		sc := fakeScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
			return image.NewRGBA(image.Rectangle{}), nil
		}}
		_, _, err := Collect(context.Background(), sc, image.Rectangle{}, 1, time.Second)
		if err == nil {
			t.Fatal("empty image accepted")
		}
	})
	t.Run("geometry change", func(t *testing.T) {
		width := 2
		sc := fakeScreenshotter{grab: func(context.Context, image.Rectangle) (image.Image, error) {
			img := image.NewRGBA(image.Rect(0, 0, width, 2))
			width++
			return img, nil
		}}
		durations, bounds, err := Collect(context.Background(), sc, image.Rectangle{}, 2, time.Second)
		var collectionErr *CollectionError
		if len(durations) != 1 || bounds.Dx() != 2 || !errors.As(err, &collectionErr) || collectionErr.Succeeded != 1 {
			t.Fatalf("durations=%d bounds=%v err=%v", len(durations), bounds, err)
		}
	})
}
