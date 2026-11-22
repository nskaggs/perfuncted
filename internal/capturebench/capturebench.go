// Package capturebench contains bounded, deterministic measurement helpers for
// screenshot benchmarks. It never persists or formats image contents.
package capturebench

import (
	"context"
	"errors"
	"fmt"
	"image"
	"reflect"
	"sort"
	"time"
)

const MinimumPercentileSamples = 20

type Screenshotter interface {
	Grab(context.Context, image.Rectangle) (image.Image, error)
}

type Distribution struct {
	Count  int
	Min    time.Duration
	P50    time.Duration
	P95    time.Duration
	Max    time.Duration
	HasP95 bool
}

type CollectionError struct {
	Sample    int
	Succeeded int
	TimedOut  bool
	Err       error
}

func (e *CollectionError) Error() string {
	return fmt.Sprintf("capture benchmark: sample %d failed after %d successful samples (timeout=%t error_type=%T)", e.Sample, e.Succeeded, e.TimedOut, e.Err)
}

func (e *CollectionError) Unwrap() error { return e.Err }

func Summarize(samples []time.Duration) (Distribution, error) {
	if len(samples) == 0 {
		return Distribution{}, errors.New("capture benchmark: no successful samples")
	}
	ordered := append([]time.Duration(nil), samples...)
	for _, sample := range ordered {
		if sample < 0 {
			return Distribution{}, fmt.Errorf("capture benchmark: negative duration %s", sample)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	d := Distribution{Count: len(ordered), Min: ordered[0], P50: ordered[nearestRankIndex(len(ordered), 50)], Max: ordered[len(ordered)-1]}
	if len(ordered) >= MinimumPercentileSamples {
		d.P95 = ordered[nearestRankIndex(len(ordered), 95)]
		d.HasP95 = true
	}
	return d, nil
}

func nearestRankIndex(count, percentile int) int { return (count*percentile+99)/100 - 1 }

func Collect(ctx context.Context, sc Screenshotter, rect image.Rectangle, count int, timeout time.Duration) ([]time.Duration, image.Rectangle, error) {
	if err := validateCollectInputs(ctx, sc, count, timeout); err != nil {
		return nil, image.Rectangle{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, image.Rectangle{}, collectionError(1, 0, err)
	}
	durations := make([]time.Duration, 0, count)
	var geometry image.Rectangle
	for i := 0; i < count; i++ {
		succeeded := len(durations)
		if err := ctx.Err(); err != nil {
			return durations, geometry, collectionError(i+1, succeeded, err)
		}
		elapsed, bounds, err := collectOne(ctx, sc, rect, timeout)
		if err != nil {
			return durations, geometry, collectionError(i+1, succeeded, err)
		}
		if i == 0 {
			geometry = bounds
		} else if bounds != geometry {
			return durations, geometry, collectionError(i+1, succeeded, fmt.Errorf("geometry changed from %v to %v", geometry, bounds))
		}
		durations = append(durations, elapsed)
	}
	return durations, geometry, nil
}

func validateCollectInputs(ctx context.Context, sc Screenshotter, count int, timeout time.Duration) error {
	if ctx == nil {
		return errors.New("capture benchmark: nil context")
	}
	if sc == nil || isNil(sc) {
		return errors.New("capture benchmark: nil screenshotter")
	}
	if count <= 0 {
		return fmt.Errorf("capture benchmark: invalid sample count %d", count)
	}
	if timeout <= 0 {
		return fmt.Errorf("capture benchmark: invalid per-capture timeout %s", timeout)
	}
	return nil
}

func collectOne(ctx context.Context, sc Screenshotter, rect image.Rectangle, timeout time.Duration) (time.Duration, image.Rectangle, error) {
	captureCtx, cancel := context.WithTimeout(ctx, timeout)
	started := time.Now()
	img, err := sc.Grab(captureCtx, rect)
	elapsed := time.Since(started)
	captureErr := captureCtx.Err()
	cancel()
	if err == nil && captureErr != nil {
		err = captureErr
	}
	if err != nil {
		return 0, image.Rectangle{}, err
	}
	if err := ctx.Err(); err != nil {
		return 0, image.Rectangle{}, err
	}
	if isNilImage(img) {
		return 0, image.Rectangle{}, errors.New("backend returned nil image")
	}
	bounds := img.Bounds()
	if bounds.Empty() {
		return 0, image.Rectangle{}, fmt.Errorf("backend returned empty bounds %v", bounds)
	}
	return elapsed, bounds, nil
}

func collectionError(sample, succeeded int, err error) *CollectionError {
	return &CollectionError{Sample: sample, Succeeded: succeeded, TimedOut: errors.Is(err, context.DeadlineExceeded), Err: err}
}

func isNil(value any) bool {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func isNilImage(img image.Image) bool { return img == nil || isNil(img) }
