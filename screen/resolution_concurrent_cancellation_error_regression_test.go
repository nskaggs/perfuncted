package screen

import (
	"context"
	"errors"
	"image"
	"testing"
)

type rejectingResolutionGrab struct {
	Screenshotter
	cancel  context.CancelFunc
	failure error
}

func (s rejectingResolutionGrab) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.cancel()
	return nil, s.failure
}

// Both the caller's cancellation and a simultaneous capture failure are
// authoritative; one must not erase the other.
func TestResolutionFromGrabRetainsConcurrentCancellationAndCaptureError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cause := errors.New("capture backend lost its output")
	_, _, err := resolutionFromGrab(ctx, rejectingResolutionGrab{cancel: cancel, failure: cause})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("lost one failure: %v", err)
	}
}

func TestValidatedResolutionRetainsConcurrentCancellationAndResolverError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cause := errors.New("output resolution changed mid-probe")
	_, _, err := validatedResolution(ctx, 1920, 1080, cause)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("lost one failure: %v", err)
	}
}
