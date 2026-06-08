package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	pollpkg "github.com/nskaggs/perfuncted/poll"
)

// errScreen always returns an error from Grab.
type errScreen struct{ err error }

func (e *errScreen) Grab(_ context.Context, _ image.Rectangle) (image.Image, error) {
	return nil, e.err
}
func (e *errScreen) GrabFullHash(_ context.Context) (uint32, error) { return 0, e.err }
func (e *errScreen) GrabRegionHash(_ context.Context, _ image.Rectangle) (uint32, error) {
	return 0, e.err
}

// noSubImage returns an image.Image that does not implement SubImage.
type noSubImage struct {
	w, h int
}

func (n *noSubImage) ColorModel() color.Model { return color.RGBAModel }
func (n *noSubImage) Bounds() image.Rectangle { return image.Rect(0, 0, n.w, n.h) }
func (n *noSubImage) At(x, y int) color.Color { return color.RGBA{R: 1, G: 2, B: 3, A: 255} }

// noSubImageScreen wraps noSubImage in a Screenshotter.
type noSubImageScreen struct{ img *noSubImage }

func (s *noSubImageScreen) Grab(_ context.Context, _ image.Rectangle) (image.Image, error) {
	return s.img, nil
}
func (s *noSubImageScreen) GrabFullHash(_ context.Context) (uint32, error) { return 1, nil }
func (s *noSubImageScreen) GrabRegionHash(_ context.Context, _ image.Rectangle) (uint32, error) {
	return 1, nil
}

// ── GrabHash error paths ─────────────────────────────────────────────────────

func TestGrabHash_ErrorFromGrab(t *testing.T) {
	boom := errors.New("grab boom")
	sc := &errScreen{err: boom}
	// Use a non-nil hasher to force the Grab path (not GrabRegionHash).
	_, err := GrabHash(context.Background(), sc, image.Rect(0, 0, 4, 4), DefaultHasher)
	if !errors.Is(err, boom) {
		t.Fatalf("GrabHash error = %v, want %v", err, boom)
	}
}

func TestGrabHash_ErrorFromGrabRegionHash(t *testing.T) {
	boom := errors.New("region hash boom")
	sc := &errScreen{err: boom}
	// nil hasher → uses GrabRegionHash
	_, err := GrabHash(context.Background(), sc, image.Rect(0, 0, 4, 4), nil)
	if !errors.Is(err, boom) {
		t.Fatalf("GrabHash (nil hasher) error = %v, want %v", err, boom)
	}
}

// ── FirstPixel / LastPixel error paths ───────────────────────────────────────

func TestFirstPixel_GrabError(t *testing.T) {
	boom := errors.New("first pixel boom")
	sc := &errScreen{err: boom}
	_, err := FirstPixel(context.Background(), sc, image.Rect(0, 0, 10, 10))
	if !errors.Is(err, boom) {
		t.Fatalf("FirstPixel error = %v, want %v", err, boom)
	}
}

func TestLastPixel_GrabError(t *testing.T) {
	boom := errors.New("last pixel boom")
	sc := &errScreen{err: boom}
	_, err := LastPixel(context.Background(), sc, image.Rect(0, 0, 10, 10))
	if !errors.Is(err, boom) {
		t.Fatalf("LastPixel error = %v, want %v", err, boom)
	}
}

// ── FindColor error paths ─────────────────────────────────────────────────────

func TestFindColor_ToleranceTooHigh(t *testing.T) {
	sc := &solidScreenshotter{}
	_, err := FindColor(context.Background(), sc, image.Rect(0, 0, 4, 4), color.RGBA{}, 256)
	if err == nil {
		t.Fatal("expected error for tolerance > 255")
	}
}

func TestFindColor_GrabError(t *testing.T) {
	boom := errors.New("find-color boom")
	sc := &errScreen{err: boom}
	_, err := FindColor(context.Background(), sc, image.Rect(0, 0, 4, 4), color.RGBA{}, 0)
	if !errors.Is(err, boom) {
		t.Fatalf("FindColor grab error = %v, want %v", err, boom)
	}
}

func TestFindColor_NotFound(t *testing.T) {
	sc := &solidScreenshotter{} // solid {0x12, 0x34, 0x56}
	_, err := FindColor(context.Background(), sc, image.Rect(0, 0, 4, 4), color.RGBA{R: 255, G: 0, B: 0, A: 255}, 0)
	if err == nil {
		t.Fatal("expected ErrNotFound for missing colour")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("FindColor not-found error = %v, want ErrNotFound", err)
	}
}

// ── WaitForFn error paths ─────────────────────────────────────────────────────

func TestWaitForFn_GrabError(t *testing.T) {
	boom := errors.New("fn boom")
	sc := &errScreen{err: boom}
	ctx := context.Background()
	_, err := WaitForFn(ctx, sc, image.Rect(0, 0, 4, 4), func(_ context.Context, _ image.Image) bool { return true }, 1*time.Millisecond)
	if !errors.Is(err, boom) {
		t.Fatalf("WaitForFn grab error = %v, want %v", err, boom)
	}
}

// ── LocateExact error/edge paths ─────────────────────────────────────────────

func TestLocateExact_EmptySearchArea(t *testing.T) {
	sc := &solidScreenshotter{}
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	_, err := LocateExact(context.Background(), sc, image.Rectangle{}, ref)
	if err == nil {
		t.Fatal("expected error for empty search area")
	}
}

func TestLocateExact_EmptyReference(t *testing.T) {
	sc := &solidScreenshotter{}
	_, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 10, 10), image.NewRGBA(image.Rectangle{}))
	if err == nil {
		t.Fatal("expected error for empty reference image")
	}
}

func TestLocateExact_ReferenceLargerThanSearchArea(t *testing.T) {
	sc := &solidScreenshotter{}
	ref := image.NewRGBA(image.Rect(0, 0, 20, 20))
	_, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 5, 5), ref)
	if err == nil {
		t.Fatal("expected error when reference larger than search area")
	}
}

func TestLocateExact_GrabError(t *testing.T) {
	boom := errors.New("locate boom")
	sc := &errScreen{err: boom}
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	_, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 10, 10), ref)
	if !errors.Is(err, boom) {
		t.Fatalf("LocateExact grab error = %v, want %v", err, boom)
	}
}

func TestLocateExact_NotFound(t *testing.T) {
	sc := &solidScreenshotter{} // solid colour; needle is different
	needle := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			needle.SetRGBA(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255}) // red
		}
	}
	_, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 8, 8), needle)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("LocateExact not-found error = %v, want ErrNotFound", err)
	}
}

// ── ScanFor error paths ───────────────────────────────────────────────────────

func TestScanFor_LenMismatch(t *testing.T) {
	sc := &solidScreenshotter{}
	rects := []image.Rectangle{image.Rect(0, 0, 4, 4)}
	_, err := ScanFor(context.Background(), sc, rects, []uint32{1, 2}, 1*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected error for len(rects) != len(wants)")
	}
}

func TestScanFor_GrabError(t *testing.T) {
	boom := errors.New("scan boom")
	sc := &errScreen{err: boom}
	// Separate rects to force per-rect grab path
	rects := []image.Rectangle{image.Rect(0, 0, 2, 2), image.Rect(100, 100, 110, 110)}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := ScanFor(ctx, sc, rects, []uint32{1, 2}, 1*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected error from grab failure")
	}
}

func TestScanFor_NoSubImageFallback(t *testing.T) {
	// noSubImageScreen makes bbox path fall back to per-rect grab path.
	// Use two adjacent rects with bbox area <= 2*totalArea.
	sc := &noSubImageScreen{img: &noSubImage{w: 20, h: 10}}
	rects := []image.Rectangle{image.Rect(0, 0, 10, 10), image.Rect(10, 0, 20, 10)}
	// wants will never match, so expect timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := ScanFor(ctx, sc, rects, []uint32{0xdeadbeef, 0xcafebabe}, 1*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected timeout error when no match")
	}
}

// ── WaitFor / WaitForChange / WaitForNoChange — Grab error paths ──────────────

func TestWaitFor_GrabError(t *testing.T) {
	boom := errors.New("wait for boom")
	sc := &errScreen{err: boom}
	_, err := WaitFor(context.Background(), sc, image.Rect(0, 0, 4, 4), 0x1234, 1*time.Millisecond, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("WaitFor grab error = %v, want %v", err, boom)
	}
}

func TestWaitForChange_GrabError(t *testing.T) {
	boom := errors.New("change boom")
	sc := &errScreen{err: boom}
	_, err := WaitForChange(context.Background(), sc, image.Rect(0, 0, 4, 4), 0, 1*time.Millisecond, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("WaitForChange grab error = %v, want %v", err, boom)
	}
}

func TestWaitForNoChange_GrabError(t *testing.T) {
	boom := errors.New("no-change boom")
	sc := &errScreen{err: boom}
	_, err := WaitForNoChange(context.Background(), sc, image.Rect(0, 0, 4, 4), 2, 1*time.Millisecond, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("WaitForNoChange grab error = %v, want %v", err, boom)
	}
}

// ── WaitForNoChangeFrom — initial hash provided (streak starts at 1) ──────────

func TestWaitForNoChangeFrom_WithInitial(t *testing.T) {
	// solidScreenshotter returns the same image each time, so streak accumulates.
	sc := &solidScreenshotter{}
	img, _ := sc.Grab(context.Background(), image.Rect(0, 0, 4, 4))
	initial := PixelHash(img, nil)

	got, err := WaitForNoChangeFrom(context.Background(), sc, image.Rect(0, 0, 4, 4), initial, 2, 1*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WaitForNoChangeFrom: unexpected error: %v", err)
	}
	if got != initial {
		t.Fatalf("WaitForNoChangeFrom: got %08x, want %08x", got, initial)
	}
}

// ── WaitWithTolerance error path ──────────────────────────────────────────────

func TestWaitWithTolerance_GrabError(t *testing.T) {
	boom := errors.New("tolerance boom")
	sc := &errScreen{err: boom}
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			ref.SetRGBA(x, y, color.RGBA{R: 1, G: 2, B: 3, A: 255})
		}
	}
	_, _, err := WaitWithTolerance(context.Background(), sc, image.Rect(0, 0, 4, 4), ref, 0, 1*time.Millisecond, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("WaitWithTolerance grab error = %v, want %v", err, boom)
	}
}

func TestWaitWithTolerance_EmptyExpectedRect(t *testing.T) {
	sc := &solidScreenshotter{}
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	_, _, err := WaitWithTolerance(context.Background(), sc, image.Rectangle{}, ref, 0, 1*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected error for empty expected rect")
	}
}

func TestWaitWithTolerance_NoSubImage_Timeout(t *testing.T) {
	// noSubImageScreen returns images that don't support SubImage.
	// LocateExactInImage falls back to the slow path; with a non-matching
	// reference the poll should time out rather than panic.
	sc := &noSubImageScreen{img: &noSubImage{w: 10, h: 10}}
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			ref.SetRGBA(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, _, err := WaitWithTolerance(ctx, sc, image.Rect(0, 0, 4, 4), ref, 0, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected timeout when reference not found")
	}
}

// ── clampPoll ─────────────────────────────────────────────────────────────────

func TestClampPoll_ZeroReturnsDefault(t *testing.T) {
	got := pollpkg.Clamp(0)
	if got != 10*time.Millisecond {
		t.Fatalf("Clamp(0) = %v, want 10ms", got)
	}
}

func TestClampPoll_NegativeReturnsDefault(t *testing.T) {
	got := pollpkg.Clamp(-1)
	if got != 10*time.Millisecond {
		t.Fatalf("Clamp(-1) = %v, want 10ms", got)
	}
}

func TestClampPoll_PositivePassThrough(t *testing.T) {
	got := pollpkg.Clamp(50 * time.Millisecond)
	if got != 50*time.Millisecond {
		t.Fatalf("Clamp(50ms) = %v, want 50ms", got)
	}
}

// ── WaitForNoChange adaptive – pixel sentinel change path ─────────────────────

// TestWaitForNoChange_AdaptivePoll_SentinelChange exercises the fast pixel
// check that resets the streak when the top-left pixel changes.
func TestWaitForNoChange_AdaptivePoll_SentinelChange(t *testing.T) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	blue := solidRGBA(color.RGBA{B: 255, A: 255})

	// Pattern: red, blue, blue, blue → streak of 3 blues should reach stable=3.
	sc := newSeqScreen(red, blue, blue, blue, blue, blue)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := WaitForNoChange(ctx, sc, image.Rect(0, 0, 4, 4), 3, 0, nil)
	if err != nil {
		t.Fatalf("WaitForNoChange sentinel change: unexpected error: %v", err)
	}
}

// TestWaitForNoChange_FixedPoll_SentinelChange exercises the sentinel pixel
// fast-reject path in the fixed-poll branch of WaitForNoChangeFrom.
func TestWaitForNoChange_FixedPoll_SentinelChange(t *testing.T) {
	red := solidRGBA(color.RGBA{R: 255, A: 255})
	blue := solidRGBA(color.RGBA{B: 255, A: 255})

	// red → blue (sentinel change) → blue → blue → stable=2
	sc := newSeqScreen(red, blue, blue, blue, blue, blue)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := WaitForNoChange(ctx, sc, image.Rect(0, 0, 4, 4), 2, 5*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WaitForNoChange fixed-poll sentinel: unexpected error: %v", err)
	}
}

// TestWaitForNoChange_FixedPoll_SentinelTimeout ensures timeout when sentinel
// keeps changing in the fixed-poll path. Uses changingScreenshotter which
// truly alternates forever (never stabilises).
func TestWaitForNoChange_FixedPoll_SentinelTimeout(t *testing.T) {
	sc := &changingScreenshotter{}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := WaitForNoChange(ctx, sc, image.Rect(0, 0, 4, 4), 5, 5*time.Millisecond, nil)
	if err == nil {
		t.Fatal("WaitForNoChange fixed-poll sentinel: expected timeout")
	}
}
