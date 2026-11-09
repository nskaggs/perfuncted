//go:build linux

package input

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type failingUinputClickReleaseTouchpad struct {
	*cancelingTouchPad
	releaseErr error
}

func (p *failingUinputClickReleaseTouchpad) LeftRelease() error {
	p.record("left-release")
	return p.releaseErr
}

// The release must be attempted even after the operation loses its context.
// The caller needs both the cancellation and the failed cleanup to decide
// whether the physical button may still be pressed on the input device.
func TestUinputClickPreservesCancellationAndButtonReleaseFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	releaseFailure := errors.New("synthetic uinput button release failed")
	touchpad := &failingUinputClickReleaseTouchpad{
		cancelingTouchPad: &cancelingTouchPad{cancel: cancel},
		releaseErr:        releaseFailure,
	}
	backend := &UinputBackend{touchpad: touchpad}
	err := backend.MouseClick(ctx, 18, 27, 1)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, releaseFailure) {
		t.Fatalf("canceled click lost cancellation or release failure: %v", err)
	}
	want := []string{"move:18,27", "left-press", "left-release"}
	if !slices.Equal(touchpad.events, want) {
		t.Fatalf("canceled click device writes = %v, want %v", touchpad.events, want)
	}
	if closeErr := backend.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}
