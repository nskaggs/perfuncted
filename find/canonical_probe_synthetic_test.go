package find

import (
	"context"
	"image"
	"testing"
)

// Count the actual adapter work across a sequence of framebuffer probes.
type canonicalProbeCounter struct {
	markerCalls int
	hashes      int
	grabs       int
}

func (s *canonicalProbeCounter) CanonicalHashing() bool {
	s.markerCalls++
	return true
}
func (s *canonicalProbeCounter) Grab(_ context.Context, _ image.Rectangle) (image.Image, error) {
	s.grabs++
	return nil, nil
}
func (s *canonicalProbeCounter) GrabFullHash(context.Context) (uint32, error) { return 17, nil }
func (s *canonicalProbeCounter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	s.hashes++
	return 17, nil
}
func TestCanonicalProbeDoesNotRecheckBackendOnEachObservation(t *testing.T) {
	screen := &canonicalProbeCounter{}
	streak := newStableStreak(0, 9)
	probe := canonicalHashProbe(context.Background(), screen, image.Rect(0, 0, 16, 16), nil, &streak)
	if probe == nil {
		t.Fatal("missing canonical probe")
	}
	for i := 0; i < 4; i++ {
		done, got, err := probe(i)
		if done || got != 17 || err != nil {
			t.Fatalf("probe %d result done=%v got=%d err=%v", i, done, got, err)
		}
	}
	if screen.markerCalls != 1 || screen.hashes != 4 || screen.grabs != 0 {
		t.Fatalf("observations marker=%d hashes=%d grabs=%d want 1/4/0", screen.markerCalls, screen.hashes, screen.grabs)
	}
}
func BenchmarkSyntheticCanonicalProbeFourObservations(b *testing.B) {
	screen := &canonicalProbeCounter{}
	ctx := context.Background()
	rect := image.Rect(0, 0, 16, 16)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		streak := newStableStreak(0, 9)
		probe := canonicalHashProbe(ctx, screen, rect, nil, &streak)
		for i := 0; i < 4; i++ {
			done, got, err := probe(i)
			if done || got != 17 || err != nil {
				b.Fatal("unexpected canonical probe")
			}
		}
	}
}
