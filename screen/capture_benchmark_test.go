package screen

import (
	"context"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/internal/capturebench"
)

type captureBenchmarkStub struct {
	grab  func(context.Context, image.Rectangle) (image.Image, error)
	grabs int
}

func (s *captureBenchmarkStub) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs++
	return s.grab(ctx, rect)
}
func (*captureBenchmarkStub) GrabFullHash(context.Context) (uint32, error) {
	panic("hash path must not be used")
}
func (*captureBenchmarkStub) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	panic("hash path must not be used")
}
func (*captureBenchmarkStub) Close() error { return nil }

func TestCaptureBenchmarkUsesGrabForFullAndRegion(t *testing.T) {
	region := image.Rect(3, 4, 11, 12)
	stub := &captureBenchmarkStub{grab: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
		switch requested {
		case image.Rectangle{}:
			return image.NewRGBA(image.Rect(0, 0, 1024, 768)), nil
		case region:
			return image.NewRGBA(region), nil
		default:
			t.Fatalf("unexpected Grab rectangle %v", requested)
			return nil, nil
		}
	}}
	for _, rect := range []image.Rectangle{{}, region} {
		durations, bounds, err := capturebench.Collect(context.Background(), stub, rect, 1, 1_000_000_000)
		if err != nil || len(durations) != 1 {
			t.Fatalf("rect=%v durations=%d err=%v", rect, len(durations), err)
		}
		if rect == (image.Rectangle{}) && bounds != image.Rect(0, 0, 1024, 768) {
			t.Fatalf("full capture bounds=%v", bounds)
		}
		if rect == region && bounds != region {
			t.Fatalf("region capture bounds=%v", bounds)
		}
	}
	if stub.grabs != 2 {
		t.Fatalf("Grab calls=%d want 2", stub.grabs)
	}
}
