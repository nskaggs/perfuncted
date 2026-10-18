package find

import (
	"context"
	"hash/crc32"
	"image"
	"testing"
	"time"
)

type canonicalPollingCounter struct {
	capabilityCalls int
	regionCalls     int
	fullCalls       int
	grabCalls       int
	seq             []uint32
	i               int
	canonical       bool
}

func (s *canonicalPollingCounter) CanonicalHashing() bool { s.capabilityCalls++; return s.canonical }
func (s *canonicalPollingCounter) next() uint32 {
	if len(s.seq) == 0 {
		return 13
	}
	i := s.i
	if i < len(s.seq)-1 {
		s.i++
	} else {
		i = len(s.seq) - 1
	}
	return s.seq[i]
}
func (s *canonicalPollingCounter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	s.regionCalls++
	return s.next(), nil
}
func (s *canonicalPollingCounter) GrabFullHash(context.Context) (uint32, error) {
	s.fullCalls++
	return s.next(), nil
}
func (s *canonicalPollingCounter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.grabCalls++
	return image.NewRGBA(image.Rect(0, 0, 8, 8)), nil
}

// Both public hash waiters should check the canonical marker once per wait,
// rather than once per observed frame.
func TestCanonicalHashWaitsSelectBackendOnlyOnce(t *testing.T) {
	rect := image.Rect(0, 0, 8, 8)
	cases := []struct {
		name     string
		full     bool
		isChange bool
	}{
		{"change-region", false, true}, {"match-region", false, false},
		{"change-full", true, true}, {"match-full", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := &canonicalPollingCounter{canonical: true, seq: []uint32{13, 13, 13, 29}}
			wanted := uint32(29)
			initial := uint32(13)
			r := rect
			if tc.full {
				r = image.Rectangle{}
			}
			var got uint32
			var err error
			if tc.isChange {
				got, err = WaitForChange(context.Background(), sc, r, initial, time.Microsecond, nil)
			} else {
				got, err = WaitFor(context.Background(), sc, r, wanted, time.Microsecond, nil)
			}
			if err != nil || got != wanted {
				t.Fatalf("wait got=%d want=%d err=%v", got, wanted, err)
			}
			if sc.capabilityCalls != 1 || sc.grabCalls != 0 || sc.regionCalls+sc.fullCalls != 4 {
				t.Fatalf("work: markers=%d grabs=%d region=%d full=%d; want 1/0/4 hash calls", sc.capabilityCalls, sc.grabCalls, sc.regionCalls, sc.fullCalls)
			}
		})
	}
}
func TestCanonicalHashWaitPreservesCustomHasher(t *testing.T) {
	sc := &canonicalPollingCounter{canonical: true}
	rect := image.Rect(0, 0, 8, 8)
	want := PixelHash(image.NewRGBA(rect), crc32.NewIEEE)
	got, err := WaitFor(context.Background(), sc, rect, want, time.Microsecond, crc32.NewIEEE)
	if err != nil || got != want {
		t.Fatalf("custom hash=%08x err=%v, want=%08x", got, err, want)
	}
	if sc.capabilityCalls != 0 || sc.regionCalls != 0 || sc.grabCalls != 1 {
		t.Fatalf("custom work markers=%d hashes=%d grabs=%d; want 0/0/1", sc.capabilityCalls, sc.regionCalls, sc.grabCalls)
	}
}
func BenchmarkCanonicalFourHashesThroughGrabHash(b *testing.B) {
	sc := &canonicalPollingCounter{canonical: true}
	rect := image.Rect(0, 0, 8, 8)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		for range 4 {
			got, err := GrabHash(ctx, sc, rect, nil)
			if err != nil || got != 13 {
				b.Fatalf("hash=%d err=%v", got, err)
			}
		}
	}
}

// This has the identical four-probe workload as the GrabHash baseline.
func BenchmarkCanonicalFourHashesCached(b *testing.B) {
	sc := &canonicalPollingCounter{canonical: true}
	rect := image.Rect(0, 0, 8, 8)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		fast := selectedHashBackend(sc, nil)
		for range 4 {
			got, err := readWaitHash(ctx, sc, fast, rect, nil)
			if err != nil || got != 13 {
				b.Fatalf("hash=%d err=%v", got, err)
			}
		}
	}
}
