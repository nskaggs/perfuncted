package find

import (
	"context"
	"image"
	"image/color"
	"testing"
	"time"
)

// seqScreen is a Screenshotter that returns a fixed sequence of images, holding
// the last frame indefinitely once the sequence is exhausted.
type seqScreen struct {
	frames []image.Image
	idx    int
}

func newSeqScreen(frames ...image.Image) *seqScreen {
	return &seqScreen{frames: frames}
}

func (s *seqScreen) next() image.Image {
	f := s.frames[s.idx]
	if s.idx < len(s.frames)-1 {
		s.idx++
	}
	return f
}

func (s *seqScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return s.next(), nil
}

func (s *seqScreen) GrabFullHash(ctx context.Context) (uint32, error) {
	return PixelHash(s.next(), nil), nil
}

func (s *seqScreen) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	return PixelHash(s.next(), nil), nil
}

// solidRGBA returns a 4×4 *image.RGBA filled with c.
func solidRGBA(c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// ── WaitFor adaptive path ─────────────────────────────────────────────────────

// TestWaitFor_AdaptivePoll_Success verifies that WaitFor with poll=0 returns the
// matching hash on the second grab (after one backoff sleep).
func TestWaitFor_AdaptivePoll_Success(t *testing.T) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	blue := solidRGBA(color.RGBA{B: 255, A: 255})
	want := PixelHash(blue, nil)

	// First grab: red (no match). Second grab: blue (match).
	sc := newSeqScreen(red, blue)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := WaitFor(ctx, sc, image.Rect(0, 0, 4, 4), want, 0, nil)
	if err != nil {
		t.Fatalf("WaitFor adaptive: unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("WaitFor adaptive: got hash %08x, want %08x", got, want)
	}
}

// TestWaitFor_AdaptivePoll_Timeout verifies that WaitFor with poll=0 times out
// when the hash never matches.
func TestWaitFor_AdaptivePoll_Timeout(t *testing.T) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	sc := newSeqScreen(red)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := WaitFor(ctx, sc, image.Rect(0, 0, 4, 4), 0xdeadbeef, 0, nil)
	if err == nil {
		t.Fatal("WaitFor adaptive: expected timeout error, got nil")
	}
}

// ── WaitForChange adaptive path ───────────────────────────────────────────────

// TestWaitForChange_AdaptivePoll_Success verifies WaitForChange with poll=0 returns
// the new hash when the screen changes.
func TestWaitForChange_AdaptivePoll_Success(t *testing.T) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	blue := solidRGBA(color.RGBA{B: 255, A: 255})
	initial := PixelHash(red, nil)
	wantChanged := PixelHash(blue, nil)

	// First grab: red (same as initial). Second grab: blue (different).
	sc := newSeqScreen(red, blue)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), initial, 0, nil)
	if err != nil {
		t.Fatalf("WaitForChange adaptive: unexpected error: %v", err)
	}
	if got != wantChanged {
		t.Fatalf("WaitForChange adaptive: got hash %08x, want %08x", got, wantChanged)
	}
}

// TestWaitForChange_AdaptivePoll_Timeout verifies WaitForChange times out when
// the screen never changes.
func TestWaitForChange_AdaptivePoll_Timeout(t *testing.T) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	initial := PixelHash(red, nil)
	sc := newSeqScreen(red)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), initial, 0, nil)
	if err == nil {
		t.Fatal("WaitForChange adaptive: expected timeout error, got nil")
	}
}

// ── WaitForNoChangeFrom adaptive path ─────────────────────────────────────────

// TestWaitForNoChange_AdaptivePoll_Success verifies WaitForNoChangeFrom with poll=0
// detects stability after two consecutive identical frames.
func TestWaitForNoChange_AdaptivePoll_Success(t *testing.T) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	wantHash := PixelHash(red, nil)

	// Two identical frames → streak reaches stable=2 and returns.
	sc := newSeqScreen(red, red)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := WaitForNoChange(ctx, sc, image.Rect(0, 0, 4, 4), 2, 0, nil)
	if err != nil {
		t.Fatalf("WaitForNoChange adaptive: unexpected error: %v", err)
	}
	if got != wantHash {
		t.Fatalf("WaitForNoChange adaptive: got hash %08x, want %08x", got, wantHash)
	}
}

// TestWaitForNoChange_AdaptivePoll_Timeout verifies WaitForNoChangeFrom times out
// when the screen keeps changing.
func TestWaitForNoChange_AdaptivePoll_Timeout(t *testing.T) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	blue := solidRGBA(color.RGBA{B: 255, A: 255})
	// Alternating frames prevent streak from accumulating.
	sc := newSeqScreen(red, blue, red, blue, red, blue)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := WaitForNoChange(ctx, sc, image.Rect(0, 0, 4, 4), 3, 0, nil)
	if err == nil {
		t.Fatal("WaitForNoChange adaptive: expected timeout error, got nil")
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

// BenchmarkWaitFor_AdaptiveHit measures WaitFor with poll=0 when the hash
// matches on the very first grab (no backoff sleep required).
func BenchmarkWaitFor_AdaptiveHit(b *testing.B) {
	img := solidRGBA(color.RGBA{R: 200, G: 100, B: 50, A: 255})
	want := PixelHash(img, nil)
	sc := newSeqScreen(img)
	rect := image.Rect(0, 0, 4, 4)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc.idx = 0 // reset frame index each iteration
		if _, err := WaitFor(ctx, sc, rect, want, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWaitForChange_AdaptiveHit measures WaitForChange with poll=0 when
// the screen has already changed (change detected on the first grab).
func BenchmarkWaitForChange_AdaptiveHit(b *testing.B) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	blue := solidRGBA(color.RGBA{B: 255, A: 255})
	initial := PixelHash(red, nil)
	sc := newSeqScreen(blue)
	rect := image.Rect(0, 0, 4, 4)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc.idx = 0
		if _, err := WaitForChange(ctx, sc, rect, initial, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWaitForNoChange_AdaptiveHit measures WaitForNoChange with poll=0
// when the screen is already stable (stable=1, first grab matches initial).
func BenchmarkWaitForNoChange_AdaptiveHit(b *testing.B) {
	img := solidRGBA(color.RGBA{G: 200, A: 255})
	initial := PixelHash(img, nil)
	sc := newSeqScreen(img)
	rect := image.Rect(0, 0, 4, 4)
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc.idx = 0
		// initial provided → streak starts at 1; stable=1 → returns on first Grab.
		if _, err := WaitForNoChangeFrom(ctx, sc, rect, initial, 1, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}
