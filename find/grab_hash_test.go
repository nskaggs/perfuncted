package find

import (
	"context"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"testing"
	"time"
)

type divergentHashScreenshotter struct {
	img         image.Image
	regionHash  uint32
	regionCalls int
}

func (s *divergentHashScreenshotter) CanonicalHashing() bool { return false }

func (s *divergentHashScreenshotter) Grab(_ context.Context, rect image.Rectangle) (image.Image, error) {
	if sub, ok := s.img.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(rect), nil
	}
	return s.img, nil
}

func (s *divergentHashScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	s.regionCalls++
	return s.regionHash, nil
}

func TestGrabHashDefaultMatchesPixelHashOfGrab(t *testing.T) {
	tests := []struct {
		name string
		img  image.Image
	}{
		{name: "RGBA backend", img: testHashImageRGBA()},
		{name: "NRGBA backend", img: testHashImageNRGBA()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &divergentHashScreenshotter{img: tt.img, regionHash: 0xdeadbeef}
			rect := image.Rect(11, 21, 13, 23)

			got, err := GrabHash(context.Background(), sc, rect, nil)
			if err != nil {
				t.Fatalf("GrabHash: %v", err)
			}
			grabbed, err := sc.Grab(context.Background(), rect)
			if err != nil {
				t.Fatalf("Grab: %v", err)
			}
			want := PixelHash(grabbed, nil)
			if got != want {
				t.Fatalf("GrabHash = %08x, want PixelHash(Grab(...)) = %08x", got, want)
			}
			if sc.regionCalls != 0 {
				t.Fatalf("GrabRegionHash calls = %d, want 0", sc.regionCalls)
			}
		})
	}
}

type canonicalHashScreenshotter struct {
	img         image.Image
	grabCalls   int
	fullCalls   int
	regionCalls int
}

func (s *canonicalHashScreenshotter) Grab(_ context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabCalls++
	return canonicalSubImage(s.img, rect), nil
}

func (s *canonicalHashScreenshotter) CanonicalHashing() bool { return true }

func (s *canonicalHashScreenshotter) GrabFullHash(context.Context) (uint32, error) {
	s.fullCalls++
	return PixelHash(s.img, nil), nil
}

func (s *canonicalHashScreenshotter) GrabRegionHash(_ context.Context, rect image.Rectangle) (uint32, error) {
	s.regionCalls++
	return PixelHash(canonicalSubImage(s.img, rect), nil), nil
}

func TestGrabHashUsesMarkedCanonicalFastPathOnlyForDefaultHasher(t *testing.T) {
	sc := &canonicalHashScreenshotter{img: testHashImageRGBA()}
	got, err := GrabHash(context.Background(), sc, image.Rect(11, 21, 13, 23), nil)
	if err != nil {
		t.Fatalf("GrabHash region: %v", err)
	}
	want := PixelHash(canonicalSubImage(sc.img, image.Rect(11, 21, 13, 23)), nil)
	if got != want || sc.regionCalls != 1 || sc.grabCalls != 0 {
		t.Fatalf("region fast path = hash %08x, region calls %d, grab calls %d; want %08x, 1, 0", got, sc.regionCalls, sc.grabCalls, want)
	}

	custom := crc32.NewIEEE
	_, err = GrabHash(context.Background(), sc, image.Rect(11, 21, 13, 23), custom)
	if err != nil {
		t.Fatalf("GrabHash custom: %v", err)
	}
	if sc.grabCalls != 1 {
		t.Fatalf("custom hasher grab calls = %d, want 1", sc.grabCalls)
	}
}

var errGrabMustNotBeCalled = errors.New("canonical fast path must not materialize an image")

// canonicalSettlingScreenshotter reports a scripted hash sequence through the
// canonical path, so a poll loop can be observed without materializing an image
// per iteration. Grab fails the test if it is reached.
type canonicalSettlingScreenshotter struct {
	hashes      []uint32
	next        int
	regionCalls int
	grabCalls   int
	canonical   bool
}

func (s *canonicalSettlingScreenshotter) hash() uint32 {
	if s.next >= len(s.hashes) {
		return s.hashes[len(s.hashes)-1]
	}
	h := s.hashes[s.next]
	s.next++
	return h
}

func (s *canonicalSettlingScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.grabCalls++
	return nil, errGrabMustNotBeCalled
}

func (s *canonicalSettlingScreenshotter) CanonicalHashing() bool { return s.canonical }

func (s *canonicalSettlingScreenshotter) GrabFullHash(context.Context) (uint32, error) {
	return s.hash(), nil
}

func (s *canonicalSettlingScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	s.regionCalls++
	return s.hash(), nil
}

// The settle loop runs at the caller's poll cadence, often full-screen. A
// canonical backend can answer it from the framebuffer in place, so it must not
// allocate an image per poll, and it must agree with WaitForChange about which
// frame it compared.
func TestWaitForNoChangeFromUsesCanonicalHashFastPath(t *testing.T) {
	t.Parallel()

	rect := image.Rect(0, 0, 4, 4)
	settle := &canonicalSettlingScreenshotter{
		hashes:    []uint32{1, 2, 2, 2, 2},
		canonical: true,
	}
	got, err := WaitForNoChangeFrom(context.Background(), settle, rect, 0, 3, time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WaitForNoChangeFrom: %v", err)
	}
	if got != 2 {
		t.Fatalf("stable hash = %08x, want 00000002", got)
	}
	if settle.grabCalls != 0 {
		t.Fatalf("grab calls = %d, want 0; the canonical path must not materialize an image", settle.grabCalls)
	}
	if settle.regionCalls == 0 {
		t.Fatal("region calls = 0, want the canonical path to answer every poll")
	}

	change := &canonicalSettlingScreenshotter{
		hashes:    []uint32{2, 2, 2, 2, 2},
		canonical: true,
	}
	before, err := GrabHash(context.Background(), change, rect, nil)
	if err != nil {
		t.Fatalf("GrabHash: %v", err)
	}
	after, err := WaitForNoChangeFrom(context.Background(), change, rect, before, 3, time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WaitForNoChangeFrom from initial: %v", err)
	}
	if after != before {
		t.Fatalf("stable hash = %08x, want the initial %08x observed unchanged", after, before)
	}
	if change.grabCalls != 0 {
		t.Fatalf("grab calls = %d, want 0", change.grabCalls)
	}
}

// A caller-supplied hasher is not interchangeable with a backend-local checksum,
// so it must keep using Grab even when the backend is marked canonical.
func TestWaitForNoChangeFromFallsBackToGrabForCustomHasher(t *testing.T) {
	t.Parallel()

	sc := &canonicalSettlingScreenshotter{
		hashes:    []uint32{1, 1, 1},
		canonical: true,
	}
	// The scripted canonical hash is irrelevant here; the custom hasher forces
	// Grab, which this double refuses.
	if _, err := WaitForNoChangeFrom(context.Background(), sc, image.Rect(0, 0, 4, 4), 0, 2, time.Millisecond, crc32.NewIEEE); !errors.Is(err, errGrabMustNotBeCalled) {
		t.Fatalf("error = %v, want the Grab fallback to be taken for a custom hasher", err)
	}
	if sc.grabCalls != 1 {
		t.Fatalf("grab calls = %d, want 1", sc.grabCalls)
	}
	if sc.regionCalls != 0 {
		t.Fatalf("region calls = %d, want 0 for a custom hasher", sc.regionCalls)
	}
}

func canonicalSubImage(img image.Image, rect image.Rectangle) image.Image {
	sub, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return img
	}
	return sub.SubImage(rect)
}

func testHashImageRGBA() image.Image {
	img := image.NewRGBA(image.Rect(10, 20, 14, 24))
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 42, A: 255})
		}
	}
	return img
}

func testHashImageNRGBA() image.Image {
	img := image.NewNRGBA(image.Rect(10, 20, 14, 24))
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 42, A: 255})
		}
	}
	return img
}
