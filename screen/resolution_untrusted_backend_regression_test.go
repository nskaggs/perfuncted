package screen

import (
	"context"
	"errors"
	"testing"
)

type cancelingLegacyResolver struct {
	resolutionResolver
	cancel context.CancelFunc
}

func (r cancelingLegacyResolver) Resolution() (int, int, error) { r.cancel(); return 900, 700, nil }

func TestResolutionWithContextRejectsNilImageWithoutPanic(t *testing.T) {
	w, h, err := ResolutionWithContext(context.Background(), &resolutionCancelScreenshotter{})
	if err == nil || w != 0 || h != 0 {
		t.Fatalf("invalid empty capture accepted: %dx%d err=%v", w, h, err)
	}
}
func TestResolutionWithContextRejectsCancellationByLegacyResolver(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w, h, err := ResolutionWithContext(ctx, cancelingLegacyResolver{cancel: cancel})
	if !errors.Is(err, context.Canceled) || w != 0 || h != 0 {
		t.Fatalf("canceled resolver accepted: %dx%d err=%v", w, h, err)
	}
}
