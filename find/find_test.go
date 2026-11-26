package find

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/util"
	pollpkg "github.com/nskaggs/perfuncted/v2/poll"
	"hash"
	"hash/adler32"
	"hash/crc32"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

type faultControlledCapture struct {
	image   image.Image
	failure error
	grabs   []image.Rectangle
}

func (s *faultControlledCapture) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs = append(s.grabs, rect)
	if s.failure != nil {
		return nil, s.failure
	}
	return s.image, nil
}

// Every high-level visual query must preserve the backend's original error,
// stop after one denied capture, and never invent a successful pixel result.
func TestVisualSearchOperationsPreserveDeniedCaptureAndNeverRetry(t *testing.T) {
	denied := errors.New("synthetic screenshot permission revoked")
	rect := image.Rect(4, 6, 8, 10)
	reference := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, tc := range []struct {
		name string
		call func(Screenshotter) error
	}{
		{"grab hash", func(s Screenshotter) error { _, e := GrabHash(context.Background(), s, rect, nil); return e }},
		{"first pixel", func(s Screenshotter) error { _, e := FirstPixel(context.Background(), s, rect); return e }},
		{"find color", func(s Screenshotter) error {
			_, e := FindColor(context.Background(), s, rect, color.RGBA{R: 12, A: 255}, 0)
			return e
		}},
		{"locate exact", func(s Screenshotter) error { _, e := LocateExact(context.Background(), s, rect, reference); return e }},
		{"wait exact hash", func(s Screenshotter) error {
			_, e := WaitFor(context.Background(), s, rect, 0, time.Hour, nil)
			return e
		}},
		{"wait change", func(s Screenshotter) error {
			_, e := WaitForChange(context.Background(), s, rect, 0, time.Hour, nil)
			return e
		}},
		{"wait settle", func(s Screenshotter) error {
			_, e := WaitForNoChangeFrom(context.Background(), s, rect, 0, 2, time.Hour, nil)
			return e
		}},
		{"scan", func(s Screenshotter) error {
			_, e := ScanFor(context.Background(), s, []image.Rectangle{rect}, []uint32{0}, time.Hour, nil)
			return e
		}},
		{"wait locate", func(s Screenshotter) error {
			_, e := WaitForLocate(context.Background(), s, rect, reference, time.Hour)
			return e
		}},
		{"wait predicate", func(s Screenshotter) error {
			_, e := WaitForFn(context.Background(), s, rect, func(context.Context, image.Image) bool { return true }, time.Hour)
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &faultControlledCapture{failure: denied}
			err := tc.call(backend)
			if !errors.Is(err, denied) {
				t.Fatalf("provider failure not preserved: %v", err)
			}
			if len(backend.grabs) != 1 {
				t.Fatalf("provider failure triggered %d screenshots", len(backend.grabs))
			}
		})
	}
}

func TestVisualSearchMalformedGenericCaptureAndOverflowCoordinates(t *testing.T) {
	empty := &faultControlledCapture{image: syntheticImage{image.NewRGBA(image.Rectangle{})}}
	pixel, err := FirstPixel(context.Background(), empty, image.Rect(10, 12, 14, 16))
	if err == nil || pixel != (color.RGBA{}) || len(empty.grabs) != 1 {
		t.Fatalf("empty generic capture yielded pixels: %+v %v (grabs=%d)", pixel, err, len(empty.grabs))
	}
	invalid := &faultControlledCapture{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	for _, rect := range []image.Rectangle{
		{Min: image.Pt(math.MaxInt, 0), Max: image.Pt(math.MaxInt, 1)},
		{Min: image.Pt(0, math.MaxInt), Max: image.Pt(1, math.MaxInt)},
	} {
		_, err := FirstPixel(context.Background(), invalid, rect)
		if err == nil {
			t.Fatalf("overflowing first pixel coordinate accepted: %v", rect)
		}
	}
	if len(invalid.grabs) != 0 {
		t.Fatalf("overflowing coordinates caused capture: %+v", invalid.grabs)
	}
}

func TestVisualScanRejectsUnboundedUnionWithoutCapturingIt(t *testing.T) {
	// Untrusted source regions can overflow their width times height; the
	// scanner must choose per-region capture rather than overflow its union.
	rect := image.Rectangle{Min: image.Pt(-math.MaxInt/2, 0), Max: image.Pt(math.MaxInt/2, 4)}
	backend := &faultControlledCapture{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	_, err := ScanFor(context.Background(), backend, []image.Rectangle{rect}, []uint32{0}, time.Hour, nil)
	if !errors.Is(err, ErrInvalidFrame) {
		t.Fatalf("unbounded region did not fail closed: %v", err)
	}
	if len(backend.grabs) != 1 || backend.grabs[0] != rect {
		t.Fatalf("unbounded scan used a different capture region: %+v", backend.grabs)
	}
}

// Synthetic BGRA frames deliberately include padding, arbitrary source
// alpha, and partial regions to keep the packed-to-canonical hash contract.
func syntheticBGRAFrame(width, height, padding int, seed int64) ([]byte, *image.RGBA, int) {
	stride := 4*width + padding
	data := make([]byte, height*stride)
	rng := rand.New(rand.NewSource(seed))
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := y*stride + x*4
			r, g, b := byte(rng.Intn(256)), byte(rng.Intn(256)), byte(rng.Intn(256))
			data[offset] = b
			data[offset+1] = g
			data[offset+2] = r
			data[offset+3] = byte(rng.Intn(256)) // compositor alpha is irrelevant
			rgba.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return data, rgba, stride
}

func TestSyntheticBGRAEquivalence(t *testing.T) {
	for _, tc := range []struct{ width, height, pad int }{
		{1, 1, 0}, {3, 9, 7}, {32, 24, 8}, {127, 11, 3}, {512, 64, 16},
	} {
		data, reference, stride := syntheticBGRAFrame(tc.width, tc.height, tc.pad, int64(tc.width*99+tc.height))
		regions := []image.Rectangle{
			image.Rectangle{}, reference.Bounds(),
			image.Rect(0, 0, 1, 1),
			image.Rect(tc.width/3, tc.height/3, tc.width, tc.height),
		}
		for _, region := range regions {
			got, err := PixelHashBGRA(data, tc.width, tc.height, stride, region)
			if err != nil {
				t.Fatalf("size=%dx%d region=%v: %v", tc.width, tc.height, region, err)
			}
			refRegion := region
			if region.Empty() {
				refRegion = reference.Bounds()
			}
			want := PixelHash(reference.SubImage(refRegion), nil)
			if got != want {
				t.Fatalf("size=%dx%d region=%v: hash %08x, want %08x", tc.width, tc.height, region, got, want)
			}
		}
	}
}

func BenchmarkSyntheticBGRA512(b *testing.B) {
	const width, height, padding = 512, 512, 16
	data, reference, stride := syntheticBGRAFrame(width, height, padding, 74)
	want := PixelHash(reference, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		got, err := PixelHashBGRA(data, width, height, stride, image.Rectangle{})
		if err != nil || got != want {
			b.Fatalf("hash=%x expected=%x err=%v", got, want, err)
		}
	}
}

// A synthetic generic image cancels the caller when the first pixel is read.
// This models a slow image adapter that finishes a read only after a session
// was revoked; the image data must not be published as a successful result.
type cancelOnPixelImage struct {
	bounds image.Rectangle
	cancel context.CancelFunc
	reads  int
}

func (i *cancelOnPixelImage) ColorModel() color.Model { return color.RGBAModel }
func (i *cancelOnPixelImage) Bounds() image.Rectangle { return i.bounds }
func (i *cancelOnPixelImage) At(x, y int) color.Color {
	i.reads++
	if i.cancel != nil {
		i.cancel()
		i.cancel = nil
	}
	return color.RGBA{R: 220, G: 110, B: 45, A: 255}
}

type pixelInspectionScreenshotter struct {
	img   image.Image
	grabs int
}

func (s *pixelInspectionScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	s.grabs++
	return s.img, nil
}

func TestFindPixelInspectionRejectsCancellationDuringActualPixelRead(t *testing.T) {
	rect := image.Rect(0, 0, 4, 4)
	pixel := color.RGBA{R: 220, G: 110, B: 45, A: 255}
	for _, tc := range []struct {
		name string
		run  func(context.Context, Screenshotter) (any, error)
	}{
		{"first pixel", func(ctx context.Context, sc Screenshotter) (any, error) {
			p, err := FirstPixel(ctx, sc, rect)
			return p, err
		}},
		{"find color", func(ctx context.Context, sc Screenshotter) (any, error) {
			p, err := FindColor(ctx, sc, rect, pixel, 0)
			return p, err
		}},
		{"locate exact", func(ctx context.Context, sc Screenshotter) (any, error) {
			reference := image.NewRGBA(image.Rect(0, 0, 1, 1))
			reference.Set(0, 0, pixel)
			p, err := LocateExact(ctx, sc, rect, reference)
			return p, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			img := &cancelOnPixelImage{bounds: rect, cancel: cancel}
			sc := &pixelInspectionScreenshotter{img: img}
			_, err := tc.run(ctx, sc)
			if img.reads == 0 || sc.grabs != 1 {
				t.Fatalf("did not exercise real pixel access, reads=%d grabs=%d", img.reads, sc.grabs)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("late private pixel result was accepted after cancellation: %v", err)
			}
		})
	}
}

// Even a provider that disregards ctx must never be called for a query that
// the client already canceled before dispatch.
func TestFindPixelOperationsNeverGrabAfterPreCancellation(t *testing.T) {
	rect := image.Rect(0, 0, 4, 4)
	pixel := color.RGBA{R: 220, G: 110, B: 45, A: 255}
	reference := image.NewRGBA(image.Rect(0, 0, 1, 1))
	reference.Set(0, 0, pixel)
	for _, tc := range []struct {
		name string
		run  func(context.Context, Screenshotter) error
	}{
		{"first pixel", func(ctx context.Context, sc Screenshotter) error { _, err := FirstPixel(ctx, sc, rect); return err }},
		{"find color", func(ctx context.Context, sc Screenshotter) error {
			_, err := FindColor(ctx, sc, rect, pixel, 0)
			return err
		}},
		{"locate exact", func(ctx context.Context, sc Screenshotter) error {
			_, err := LocateExact(ctx, sc, rect, reference)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			sc := &pixelInspectionScreenshotter{img: reference}
			err := tc.run(ctx, sc)
			if !errors.Is(err, context.Canceled) || sc.grabs != 0 {
				t.Fatalf("canceled operation contacted screenshot backend: grabs=%d err=%v", sc.grabs, err)
			}
		})
	}
}

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

// A remote screenshot provider may clip a requested desktop rectangle to a
// smaller buffer. Hashing that partial buffer against the caller's full region
// can satisfy a watch for the wrong pixels, including on compact union scans.
func TestFindRejectsPartialCapturedRegionGeometryWithoutFalseMatches(t *testing.T) {
	partial := image.NewRGBA(image.Rect(0, 0, 1, 1))
	partial.SetRGBA(0, 0, color.RGBA{R: 42, G: 100, B: 110, A: 255})
	hash := PixelHash(partial, nil)
	capture := malformedCaptureBackend{frame: partial}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"direct region hash", func() error {
			_, err := GrabHash(context.Background(), capture, image.Rect(10, 10, 12, 12), nil)
			return err
		}},
		{"compact scan", func() error {
			_, err := ScanFor(context.Background(), capture, []image.Rectangle{image.Rect(10, 10, 11, 11), image.Rect(11, 10, 12, 11)}, []uint32{hash, hash}, 0, nil)
			return err
		}},
		{"sparse scan", func() error {
			_, err := ScanFor(context.Background(), capture, []image.Rectangle{image.Rect(10, 10, 12, 12), image.Rect(100, 100, 102, 102)}, []uint32{hash, hash}, 0, nil)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("partial capture accepted as full region match: %v", err)
			}
		})
	}
}

// All visual reads must fail before attempting a capture when the backend is
// unavailable. Each operation has a distinct authorization entrypoint.
func TestVisualQueriesRejectUnavailableScreenshotterBeforeObservation(t *testing.T) {
	bounds := image.Rect(0, 0, 2, 2)
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"first pixel", func() error { _, e := FirstPixel(context.Background(), nil, bounds); return e }},
		{"wait for hash", func() error { _, e := WaitFor(context.Background(), nil, bounds, 1, time.Second, nil); return e }},
		{"wait for change", func() error { _, e := WaitForChange(context.Background(), nil, bounds, 1, time.Second, nil); return e }},
		{"wait for stable", func() error {
			_, e := WaitForNoChange(context.Background(), nil, bounds, 2, time.Second, nil)
			return e
		}},
		{"wait from known frame", func() error {
			_, e := WaitForNoChangeFrom(context.Background(), nil, bounds, 1, 2, time.Second, nil)
			return e
		}},
		{"scan", func() error {
			_, e := ScanFor(context.Background(), nil, []image.Rectangle{bounds}, []uint32{1}, time.Second, nil)
			return e
		}},
		{"locate exact", func() error { _, e := LocateExact(context.Background(), nil, bounds, ref); return e }},
		{"find color", func() error { _, e := FindColor(context.Background(), nil, bounds, color.RGBA{A: 255}, 0); return e }},
		{"wait locate", func() error { _, e := WaitForLocate(context.Background(), nil, bounds, ref, time.Second); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, util.ErrNotAvailable) {
				t.Fatalf("missing screen result: %v", err)
			}
		})
	}
}

type cancelingFindScreen struct {
	image  image.Image
	cancel context.CancelFunc
	grabs  int
}

func (s *cancelingFindScreen) Grab(_ context.Context, _ image.Rectangle) (image.Image, error) {
	s.grabs++
	s.cancel()
	return s.image, nil
}

func TestVisualQueriesDropCaptureWhenCallerRevokesWhileGrabReturns(t *testing.T) {
	rect := image.Rect(0, 0, 2, 2)
	picture := image.NewRGBA(rect)
	for _, tc := range []struct {
		name string
		run  func(context.Context, *cancelingFindScreen) error
	}{
		{"first pixel", func(c context.Context, s *cancelingFindScreen) error { _, e := FirstPixel(c, s, rect); return e }},
		{"find color", func(c context.Context, s *cancelingFindScreen) error {
			_, e := FindColor(c, s, rect, color.RGBA{A: 255}, 0)
			return e
		}},
		{"locate exact", func(c context.Context, s *cancelingFindScreen) error {
			_, e := LocateExact(c, s, rect, image.NewRGBA(image.Rect(0, 0, 1, 1)))
			return e
		}},
		{"scan", func(c context.Context, s *cancelingFindScreen) error {
			_, e := ScanFor(c, s, []image.Rectangle{rect}, []uint32{PixelHash(picture, nil)}, time.Second, nil)
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			screen := &cancelingFindScreen{image: picture, cancel: cancel}
			err := tc.run(ctx, screen)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled source exposed pixels: %v", err)
			}
			if screen.grabs != 1 {
				t.Fatalf("invalid capture count=%d", screen.grabs)
			}
		})
	}
}

func TestFindPollingAndStableStreakFailClosedAtCancellationBoundary(t *testing.T) {
	var nilCtx context.Context
	if err := contextErr(nilCtx); err != nil {
		t.Fatalf("nil internal context not normalized: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	count := 0
	value, err := poll(ctx, 0, 17, func(int) (bool, uint32, error) { count++; cancel(); return true, 83, nil })
	if !errors.Is(err, context.Canceled) || value != 83 || count != 1 {
		t.Fatalf("adaptive post-poll cancellation: result=%d err=%v count=%d", value, err, count)
	}
	streak := newStableStreak(0, 0)
	if streak.stable != 1 {
		t.Fatalf("non-positive stable streak not clamped: %+v", streak)
	}
	empty := &faultControlledCapture{image: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	_, err = WaitForNoChange(nilCtx, empty, image.Rectangle{}, 0, 0, nil)
	if err == nil {
		t.Fatal("empty-region stable observer unexpectedly succeeded")
	}
}

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
	for y := range 4 {
		for x := range 4 {
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
	for b.Loop() {
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
	for b.Loop() {
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
	for b.Loop() {
		sc.idx = 0
		// initial provided → streak starts at 1; stable=1 → returns on first Grab.
		if _, err := WaitForNoChangeFrom(ctx, sc, rect, initial, 1, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPixelHashRGBA(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := range 512 {
		for x := range 512 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = PixelHash(img, nil)
	}
}

func BenchmarkLocateExactRGBA(b *testing.B) {
	src := image.NewRGBA(image.Rect(0, 0, 256, 256))
	ref := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 256 {
		for x := range 256 {
			src.SetRGBA(x, y, color.RGBA{R: 50, G: 60, B: 70, A: 255})
		}
	}
	for y := range 16 {
		for x := range 16 {
			c := color.RGBA{R: uint8(x * 3), G: uint8(y * 5), B: 99, A: 255}
			src.SetRGBA(180+x, 160+y, c)
			ref.SetRGBA(x, y, c)
		}
	}
	sc := &fakeScreen{img: src}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 256, 256), ref); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLocateExactCommonAnchorMissRGBA(b *testing.B) {
	const sourceSize, referenceSize = 512, 32
	src := image.NewRGBA(image.Rect(0, 0, sourceSize, sourceSize))
	ref := image.NewRGBA(image.Rect(0, 0, referenceSize, referenceSize))
	for y := 1; y < referenceSize; y++ {
		for x := range referenceSize {
			ref.SetRGBA(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 5), B: uint8(x + y), A: 255})
		}
	}
	// The reference begins with a flat background row, while later rows have
	// detail that cannot match. This models a missing visual target over a flat
	// desktop region.

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := LocateExactInImage(src, src.Bounds(), ref); !errors.Is(err, ErrNotFound) {
			b.Fatalf("LocateExactInImage error = %v, want ErrNotFound", err)
		}
	}
}

func BenchmarkScanForCompact(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := range 128 {
		for x := range 128 {
			img.SetRGBA(x, y, color.RGBA{R: 25, G: 25, B: 25, A: 255})
		}
	}
	img.SetRGBA(16, 16, color.RGBA{R: 200, G: 0, B: 0, A: 255})
	img.SetRGBA(80, 80, color.RGBA{R: 0, G: 200, B: 0, A: 255})

	sc := &fakeScreen{img: img}
	rects := []image.Rectangle{image.Rect(0, 0, 32, 32), image.Rect(64, 64, 96, 96)}
	wants := make([]uint32, len(rects))
	for i, r := range rects {
		wants[i] = PixelHash(img.SubImage(r), nil)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := ScanFor(context.Background(), sc, rects, wants, 0, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWaitForFn_ImmediatelyTrue measures the latency floor when the
// predicate is satisfied on the very first poll.
func BenchmarkWaitForFn_ImmediatelyTrue(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	sc := &fakeScreen{img: img}
	rect := image.Rect(0, 0, 64, 64)
	pred := func(_ context.Context, _ image.Image) bool { return true }

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := WaitForFn(context.Background(), sc, rect, pred, 10*time.Millisecond); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWaitForFn_FivePollIterations measures cost when predicate becomes
// true after 5 grabs (exercises the poll loop).
func BenchmarkWaitForFn_FivePollIterations(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	sc := &fakeScreen{img: img}
	rect := image.Rect(0, 0, 64, 64)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		count := 0
		pred := func(_ context.Context, _ image.Image) bool {
			count++
			return count >= 5
		}
		if _, err := WaitForFn(context.Background(), sc, rect, pred, 1*time.Microsecond); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWaitFor_ImmediatelyTrue measures WaitFor when the hash matches immediately.
func BenchmarkWaitFor_ImmediatelyTrue(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 42, A: 255})
		}
	}
	sc := &fakeScreen{img: img}
	rect := image.Rect(0, 0, 64, 64)
	want := PixelHash(img, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := WaitFor(context.Background(), sc, rect, want, 1*time.Millisecond, nil); err != nil {
			b.Fatal(err)
		}
	}
}

// A backend may report cancellation before publishing even its first frame.
// In that case the only known hash is the caller's initial hash: a zero
// result incorrectly claims a pixel observation that never happened.
type noFrameCanceledScreenshotter struct {
	cancel context.CancelFunc
	reads  int
}

func (s *noFrameCanceledScreenshotter) Grab(ctx context.Context, _ image.Rectangle) (image.Image, error) {
	s.reads++
	s.cancel()
	return nil, ctx.Err()
}
func (s *noFrameCanceledScreenshotter) GrabFullHash(context.Context) (uint32, error) {
	panic("not supported")
}
func (s *noFrameCanceledScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	panic("not supported")
}
func (s *noFrameCanceledScreenshotter) Close() error { return nil }

func TestWaitForChangeDoesNotReplaceKnownHashWhenFirstGrabCancelsWithoutFrame(t *testing.T) {
	initial := uint32(0x1234feed)
	for _, pollInterval := range []time.Duration{5 * time.Millisecond, 0} {
		t.Run(pollInterval.String(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &noFrameCanceledScreenshotter{cancel: cancel}
			got, err := WaitForChange(ctx, source, image.Rect(0, 0, 4, 4), initial, pollInterval, nil)
			if !errors.Is(err, context.Canceled) || got != initial {
				t.Fatalf("missing first sample was reported as hash %08x with error %v, want last known %08x", got, err, initial)
			}
			if source.reads != 1 {
				t.Fatalf("expected exactly one failed attempt, got %d", source.reads)
			}
		})
	}
}

// TestWaitForChange_TimeoutReturnsLastHash verifies that WaitForChange returns
// the last observed hash on timeout instead of zero.
//
// WaitFor returns the last observed hash on timeout (for debugging); WaitForChange
// must do the same. Prior to the fix, both the fixed-interval and adaptive poll
// paths returned 0 on timeout, making it impossible for callers to distinguish
// "region was always zero-hash" from "region had some content but didn't change".
func TestWaitForChange_TimeoutReturnsLastHash(t *testing.T) {
	sc := &solidScreenshotter{} // always returns the same image

	img, err := sc.Grab(context.Background(), image.Rect(0, 0, 4, 4))
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	initial := PixelHash(img, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	got, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), initial, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("WaitForChange: expected timeout error, got nil")
	}
	// The return value must equal the last observed hash (not 0).
	if got == 0 {
		t.Fatalf("WaitForChange: returned 0 on timeout; want last observed hash %08x", initial)
	}
	if got != initial {
		t.Fatalf("WaitForChange: timeout returned %08x, want %08x", got, initial)
	}
}

// TestWaitForChange_AdaptivePollTimeoutReturnsLastHash is the same assertion
// for the adaptive-poll path (poll ≤ 0).
func TestWaitForChange_AdaptivePollTimeoutReturnsLastHash(t *testing.T) {
	sc := &solidScreenshotter{}

	img, err := sc.Grab(context.Background(), image.Rect(0, 0, 4, 4))
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	initial := PixelHash(img, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// poll=0 triggers adaptive-poll path.
	got, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), initial, 0, nil)
	if err == nil {
		t.Fatal("WaitForChange (adaptive): expected timeout error, got nil")
	}
	if got == 0 {
		t.Fatalf("WaitForChange (adaptive): returned 0 on timeout; want last observed hash %08x", initial)
	}
	if got != initial {
		t.Fatalf("WaitForChange (adaptive): timeout returned %08x, want %08x", got, initial)
	}
}

type cancelWhenDoneContext struct {
	done   <-chan struct{}
	cancel context.CancelFunc
	once   func()
}

func (c *cancelWhenDoneContext) Done() <-chan struct{} {
	c.once()
	return c.done
}

func (c *cancelWhenDoneContext) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func (c *cancelWhenDoneContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (c *cancelWhenDoneContext) Value(key any) any { return nil }

type cancelOnGrabScreenshotter struct {
	img    image.Image
	cancel context.CancelFunc
	grabs  int
}

func (s *cancelOnGrabScreenshotter) cancelOnce() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *cancelOnGrabScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	s.grabs++
	s.cancelOnce()
	return s.img, nil
}

func (s *cancelOnGrabScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	s.grabs++
	s.cancelOnce()
	return PixelHash(s.img, nil), nil
}

func (s *cancelOnGrabScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	s.grabs++
	s.cancelOnce()
	return PixelHash(s.img, nil), nil
}

func TestWaitFor_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{R: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitFor(ctx, sc, image.Rect(0, 0, 4, 4), want, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitFor error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitFor returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForChange_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{G: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), 0, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForChange error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitForChange returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForChange_CanceledContextBeforeGrabReturnsInitial(t *testing.T) {
	img := solidRGBA(color.RGBA{G: 255, A: 255})
	initial := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sc := &cancelOnGrabScreenshotter{img: img}

	got, err := WaitForChange(ctx, sc, image.Rect(0, 0, 4, 4), initial, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForChange error = %v, want context.Canceled", err)
	}
	if got != initial {
		t.Fatalf("WaitForChange returned %08x, want initial hash %08x", got, initial)
	}
	if sc.grabs != 0 {
		t.Fatalf("WaitForChange grabbed %d frames after context cancellation, want 0", sc.grabs)
	}
}

func TestWaitForNoChange_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{B: 255, A: 255})
	want := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForNoChangeFrom(ctx, sc, image.Rect(0, 0, 4, 4), want, 1, 10*time.Millisecond, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForNoChange error = %v, want context.Canceled", err)
	}
	if got != want {
		t.Fatalf("WaitForNoChange returned %08x, want last hash %08x", got, want)
	}
}

func TestWaitForNoChange_CanceledContextBeforeGrabReturnsInitial(t *testing.T) {
	img := solidRGBA(color.RGBA{B: 255, A: 255})
	initial := PixelHash(img, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		poll time.Duration
	}{
		{name: "fixed poll", poll: 10 * time.Millisecond},
		{name: "adaptive poll", poll: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := &cancelOnGrabScreenshotter{img: img}

			got, err := WaitForNoChangeFrom(ctx, sc, image.Rect(0, 0, 4, 4), initial, 2, tt.poll, nil)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("WaitForNoChange error = %v, want context.Canceled", err)
			}
			if got != initial {
				t.Fatalf("WaitForNoChange returned %08x, want initial hash %08x", got, initial)
			}
			if sc.grabs != 0 {
				t.Fatalf("WaitForNoChange grabbed %d frames after context cancellation, want 0", sc.grabs)
			}
		})
	}
}

func TestScanFor_CanceledContextDuringPollReturnsContextError(t *testing.T) {
	img := solidRGBA(color.RGBA{R: 1, G: 2, B: 3, A: 255})
	base, cancel := context.WithCancel(context.Background())
	ctx := &cancelWhenDoneContext{
		done:   base.Done(),
		cancel: cancel,
		once:   sync.OnceFunc(cancel),
	}

	_, err := ScanFor(
		ctx,
		&solidScreenshotter{},
		[]image.Rectangle{image.Rect(0, 0, 4, 4)},
		[]uint32{PixelHash(img, nil) + 1},
		time.Hour,
		nil,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanFor error = %v, want context.Canceled", err)
	}
}

func TestWaitForFn_CanceledContextAfterGrab(t *testing.T) {
	img := solidRGBA(color.RGBA{R: 200, G: 100, B: 50, A: 255})
	ctx, cancel := context.WithCancel(context.Background())
	sc := &cancelOnGrabScreenshotter{img: img, cancel: cancel}

	got, err := WaitForFn(ctx, sc, image.Rect(0, 0, 4, 4), func(context.Context, image.Image) bool {
		return true
	}, 10*time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForFn error = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("WaitForFn returned image on cancellation, want nil")
	}
}

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

func TestGrabHash_NilHasherErrorFromGrab(t *testing.T) {
	boom := errors.New("grab boom")
	sc := &errScreen{err: boom}
	// The default hasher uses the same Grab path as a custom hasher.
	_, err := GrabHash(context.Background(), sc, image.Rect(0, 0, 4, 4), nil)
	if !errors.Is(err, boom) {
		t.Fatalf("GrabHash with nil hasher error = %v, want %v", err, boom)
	}
}

// ── FirstPixel error paths ────────────────────────────────────────────────────

func TestFirstPixel_GrabError(t *testing.T) {
	boom := errors.New("first pixel boom")
	sc := &errScreen{err: boom}
	_, err := FirstPixel(context.Background(), sc, image.Rect(0, 0, 10, 10))
	if !errors.Is(err, boom) {
		t.Fatalf("FirstPixel error = %v, want %v", err, boom)
	}
}

func TestFirstPixelRejectsEmptyRectangle(t *testing.T) {
	sc := &solidScreenshotter{}
	if _, err := FirstPixel(context.Background(), sc, image.Rectangle{}); err == nil {
		t.Fatal("FirstPixel accepted an empty rectangle")
	}
}

func TestWaitForFnRejectsNilPredicate(t *testing.T) {
	sc := &solidScreenshotter{}
	if _, err := WaitForFn(context.Background(), sc, image.Rect(0, 0, 1, 1), nil, time.Millisecond); err == nil {
		t.Fatal("WaitForFn accepted a nil predicate")
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
	for y := range 2 {
		for x := range 2 {
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

type nilImageScreenshotter struct{}

func (nilImageScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return nil, nil
}

func (nilImageScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (nilImageScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func (nilImageScreenshotter) Close() error { return nil }

type emptyImageScreenshotter struct{}

func (emptyImageScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewRGBA(image.Rectangle{}), nil
}

func (emptyImageScreenshotter) GrabFullHash(context.Context) (uint32, error) { return 0, nil }

func (emptyImageScreenshotter) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}

func TestNilScreenshotterRejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		sc   Screenshotter
	}{
		{name: "nil interface", sc: nil},
		{name: "typed nil", sc: Screenshotter((*solidScreenshotter)(nil))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := GrabHash(context.Background(), tc.sc, image.Rect(0, 0, 1, 1), nil); err == nil {
				t.Fatal("GrabHash succeeded unexpectedly")
			}
			if _, err := WaitForFn(context.Background(), tc.sc, image.Rect(0, 0, 1, 1), func(context.Context, image.Image) bool { return true }, time.Millisecond); err == nil {
				t.Fatal("WaitForFn succeeded unexpectedly")
			}
		})
	}
}

func TestNilImageFromGrabRejected(t *testing.T) {
	sc := nilImageScreenshotter{}
	ctx := context.Background()
	rect := image.Rect(0, 0, 2, 2)
	reference := image.NewRGBA(rect)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "GrabHash",
			call: func() error {
				_, err := GrabHash(ctx, sc, rect, DefaultHasher)
				return err
			},
		},
		{
			name: "FirstPixel",
			call: func() error {
				_, err := FirstPixel(ctx, sc, rect)
				return err
			},
		},
		{
			name: "LocateExact",
			call: func() error {
				_, err := LocateExact(ctx, sc, rect, reference)
				return err
			},
		},
		{
			name: "FindColor",
			call: func() error {
				_, err := FindColor(ctx, sc, rect, color.RGBA{}, 0)
				return err
			},
		},
		{
			name: "ScanFor",
			call: func() error {
				_, err := ScanFor(ctx, sc, []image.Rectangle{rect}, []uint32{0}, time.Millisecond, DefaultHasher)
				return err
			},
		},
		{
			name: "WaitForFn",
			call: func() error {
				_, err := WaitForFn(ctx, sc, rect, func(context.Context, image.Image) bool { return true }, time.Millisecond)
				return err
			},
		},
		{
			name: "WaitForNoChangeFrom",
			call: func() error {
				_, err := WaitForNoChangeFrom(ctx, sc, rect, 0, 1, time.Millisecond, DefaultHasher)
				return err
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("operation succeeded with nil image")
			} else if errors.Is(err, context.Canceled) {
				t.Fatalf("operation returned cancellation for nil image: %v", err)
			}
		})
	}

	if got := PixelHash(nil, nil); got != 0 {
		t.Fatalf("PixelHash(nil) = %d, want safe zero hash", got)
	}
}

func TestEmptyImageFromGrabRejectedByWaitForNoChange(t *testing.T) {
	_, err := WaitForNoChangeFrom(
		context.Background(),
		emptyImageScreenshotter{},
		image.Rect(0, 0, 2, 2),
		0,
		1,
		time.Millisecond,
		DefaultHasher,
	)
	if err == nil {
		t.Fatal("WaitForNoChangeFrom succeeded with an empty image")
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("WaitForNoChangeFrom returned cancellation for empty image: %v", err)
	}
}

// TestWaitForNoChange_TimeoutReturnsLastHash verifies that WaitForNoChangeFrom
// returns the last computed hash on timeout instead of zero when the fast
// sentinel path keeps skipping full hashes.
func TestWaitForNoChange_TimeoutReturnsLastHash(t *testing.T) {
	want := PixelHash(solidRGBA(color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}), nil)

	tests := []struct {
		name string
		poll time.Duration
	}{
		{name: "fixed poll", poll: 5 * time.Millisecond},
		{name: "adaptive poll", poll: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sc := &changingScreenshotter{}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			got, err := WaitForNoChangeFrom(ctx, sc, image.Rect(0, 0, 4, 4), want, 5, tc.poll, nil)
			if err == nil {
				t.Fatal("WaitForNoChangeFrom: expected timeout error, got nil")
			}
			if got == 0 {
				t.Fatal("WaitForNoChangeFrom: returned 0 on timeout; want last observed hash")
			}
			if got != want {
				t.Fatalf("WaitForNoChangeFrom: timeout returned %08x, want %08x", got, want)
			}
		})
	}
}

// nrgbaScreen returns *image.NRGBA images, exercising all non-RGBA slow paths.
type nrgbaScreen struct {
	img *image.NRGBA
}

func (s *nrgbaScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return s.img, nil
}

func (s *nrgbaScreen) GrabFullHash(ctx context.Context) (uint32, error) {
	return PixelHash(s.img, nil), nil
}

func (s *nrgbaScreen) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	return PixelHash(s.img, nil), nil
}

// solidNRGBA returns a w×h *image.NRGBA filled with c.
func solidNRGBA(w, h int, c color.RGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	nc := color.NRGBA(c)
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, nc)
		}
	}
	return img
}

// ── colorClose (via PixelFound slow path) ────────────────────────────────────

// TestColorClose exercises the colorClose helper through the PixelFound slow
// path, which is only reached when img is not *image.RGBA.
func TestColorClose(t *testing.T) {
	img := solidNRGBA(4, 4, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	rect := img.Bounds()

	// Within tolerance: each channel within tol=10.
	target := color.RGBA{R: 205, G: 95, B: 55, A: 255}
	if _, ok := PixelFound(img, rect, target, 10); !ok {
		t.Error("expected PixelFound (slow path) to find pixel within tolerance=10")
	}

	// Exactly at tolerance boundary.
	target2 := color.RGBA{R: 210, G: 90, B: 40, A: 255}
	if _, ok := PixelFound(img, rect, target2, 10); !ok {
		t.Error("expected PixelFound to find pixel at exact tolerance boundary")
	}

	// Outside tolerance: R channel exceeds tol.
	target3 := color.RGBA{R: 215, G: 100, B: 50, A: 255}
	if _, ok := PixelFound(img, rect, target3, 10); ok {
		t.Error("expected PixelFound to NOT find pixel outside tolerance")
	}
}

// ── PixelFound slow path ──────────────────────────────────────────────────────

// TestPixelFound_SlowPath verifies PixelFound with a non-RGBA source image.
func TestPixelFound_SlowPath(t *testing.T) {
	img := solidNRGBA(8, 8, color.RGBA{R: 50, G: 50, B: 50, A: 255})
	// Place a distinctive red pixel at (3, 5).
	img.SetNRGBA(3, 5, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	rect := img.Bounds()

	pt, ok := PixelFound(img, rect, color.RGBA{R: 255, G: 0, B: 0, A: 255}, 0)
	if !ok {
		t.Fatal("PixelFound slow path: expected to find red pixel")
	}
	if pt.X != 3 || pt.Y != 5 {
		t.Fatalf("PixelFound slow path: got (%d,%d), want (3,5)", pt.X, pt.Y)
	}

	// Not found case.
	if _, ok := PixelFound(img, rect, color.RGBA{R: 0, G: 255, B: 0, A: 255}, 0); ok {
		t.Fatal("PixelFound slow path: expected no match for green in a non-green image")
	}
}

// TestPixelFound_SlowPath_AbsoluteCoord verifies that the returned point is in
// absolute screen coordinates when the image rect is offset.
func TestPixelFound_SlowPath_AbsoluteCoord(t *testing.T) {
	// Allocate image at offset (10, 20) to simulate absolute-bounds images.
	img := image.NewNRGBA(image.Rect(10, 20, 14, 24))
	img.SetNRGBA(12, 22, color.NRGBA{R: 0, G: 0, B: 200, A: 255})

	// rect argument tells PixelFound the absolute capture bounds.
	captureRect := image.Rect(10, 20, 14, 24)
	pt, ok := PixelFound(img, captureRect, color.RGBA{R: 0, G: 0, B: 200, A: 255}, 0)
	if !ok {
		t.Fatal("PixelFound slow path (offset): expected to find blue pixel")
	}
	// PixelFound maps (x=12, y=22) → abs = rect.Min + (x-b.Min.X, y-b.Min.Y)
	//   = (10,20) + (12-10, 22-20) = (12, 22).
	if pt.X != 12 || pt.Y != 22 {
		t.Fatalf("PixelFound slow path (offset): got (%d,%d), want (12,22)", pt.X, pt.Y)
	}
}

// ── matchAt slow path ─────────────────────────────────────────────────────────

// TestMatchAt_SlowPath verifies matchAt with non-RGBA (NRGBA) images.
func TestMatchAt_SlowPath(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	ref := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	// Fill a 2×2 patch in src at (4, 6).
	for y := range 2 {
		for x := range 2 {
			c := color.NRGBA{R: uint8(x*100 + 50), G: uint8(y*100 + 50), B: 0, A: 255}
			src.SetNRGBA(4+x, 6+y, c)
			ref.SetNRGBA(x, y, c)
		}
	}

	if !matchAt(src, ref, 4, 6) {
		t.Fatal("matchAt slow path: should match at (4,6)")
	}
	if matchAt(src, ref, 0, 0) {
		t.Fatal("matchAt slow path: should not match at (0,0)")
	}
}

// ── LocateExact slow path ──────────────────────────────────────────────────────

// TestLocateExact_SlowPath verifies LocateExact when both src and ref are non-RGBA.
func TestLocateExact_SlowPath(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	ref := image.NewNRGBA(image.Rect(0, 0, 3, 3))

	// Fill a 3×3 pattern in src at (7, 9) and the reference.
	for y := range 3 {
		for x := range 3 {
			c := color.NRGBA{R: 180, G: uint8(x * 70), B: uint8(y * 70), A: 255}
			src.SetNRGBA(7+x, 9+y, c)
			ref.SetNRGBA(x, y, c)
		}
	}

	sc := &nrgbaScreen{img: src}
	found, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 20, 20), ref)
	if err != nil {
		t.Fatalf("LocateExact slow path: unexpected error: %v", err)
	}
	if found.Min.X != 7 || found.Min.Y != 9 {
		t.Fatalf("LocateExact slow path: got min (%d,%d), want (7,9)", found.Min.X, found.Min.Y)
	}
	if found.Dx() != 3 || found.Dy() != 3 {
		t.Fatalf("LocateExact slow path: got size (%d,%d), want (3,3)", found.Dx(), found.Dy())
	}
}

// ── Benchmarks ────────────────────────────────────────────────────────────────

// BenchmarkPixelFound_FastPath benchmarks PixelFound with *image.RGBA (fast path).
func BenchmarkPixelFound_FastPath(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := range 256 {
		for x := range 256 {
			img.SetRGBA(x, y, color.RGBA{R: 80, G: 80, B: 80, A: 255})
		}
	}
	img.SetRGBA(200, 200, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	target := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	rect := img.Bounds()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = PixelFound(img, rect, target, 0)
	}
}

// BenchmarkPixelFound_NRGBA benchmarks the native screenshot image layout.
func BenchmarkPixelFound_NRGBA(b *testing.B) {
	img := solidNRGBA(256, 256, color.RGBA{R: 80, G: 80, B: 80, A: 255})
	img.SetNRGBA(200, 200, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
	target := color.RGBA{R: 255, G: 0, B: 0, A: 255}
	rect := img.Bounds()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = PixelFound(img, rect, target, 0)
	}
}

func TestPixelFoundNRGBAAllocations(t *testing.T) {
	img := solidNRGBA(256, 256, color.RGBA{R: 80, G: 80, B: 80, A: 255})
	target := color.RGBA{R: 255, G: 0, B: 0, A: 255}

	allocs := testing.AllocsPerRun(100, func() {
		_, _ = PixelFound(img, img.Bounds(), target, 0)
	})
	if allocs != 0 {
		t.Fatalf("PixelFound NRGBA allocations = %v, want 0", allocs)
	}
}

// ── PixelHash ─────────────────────────────────────────────────────────────────

func TestPixelHashDeterministic(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 50), G: uint8(y * 50), B: 100, A: 255})
		}
	}
	h1 := PixelHash(img, nil)
	h2 := PixelHash(img, nil)
	if h1 != h2 {
		t.Fatalf("same image gave different hashes: %08x vs %08x", h1, h2)
	}
}

func TestPixelHashDiffersForDifferentImages(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	b := image.NewRGBA(image.Rect(0, 0, 2, 2))
	b.SetRGBA(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})

	ha := PixelHash(a, nil)
	hb := PixelHash(b, nil)
	if ha == hb {
		t.Fatal("different images should (almost certainly) have different hashes")
	}
}

func TestPixelHashSubImage(t *testing.T) {
	full := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := range 10 {
		for x := range 10 {
			full.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 42, A: 255})
		}
	}
	sub := full.SubImage(image.Rect(2, 2, 5, 5)).(*image.RGBA) //nolint:errcheck // SubImage on *image.RGBA always returns *image.RGBA

	// Build an equivalent standalone image.
	equiv := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for y := range 3 {
		for x := range 3 {
			equiv.SetRGBA(x, y, full.RGBAAt(x+2, y+2))
		}
	}

	hSub := PixelHash(sub, nil)
	hEquiv := PixelHash(equiv, nil)
	if hSub != hEquiv {
		t.Fatalf("subimage hash %08x != equivalent %08x", hSub, hEquiv)
	}
}

func TestPixelHashFullWidthSubImage(t *testing.T) {
	const width = 4
	full := image.NewRGBA(image.Rect(0, 0, width, 4))
	for y := range 4 {
		for x := range width {
			full.SetRGBA(x, y, color.RGBA{R: uint8(y + 1), G: uint8(x), A: 255})
		}
	}

	sub := full.SubImage(image.Rect(0, 1, width, 2)).(*image.RGBA) //nolint:errcheck // SubImage on *image.RGBA always returns *image.RGBA
	equiv := image.NewRGBA(image.Rect(0, 0, width, 1))
	for x := range width {
		equiv.SetRGBA(x, 0, full.RGBAAt(x, 1))
	}

	if got, want := PixelHash(sub, nil), PixelHash(equiv, nil); got != want {
		t.Fatalf("full-width subimage hash %08x != equivalent hash %08x", got, want)
	}
}

// ── FirstPixel ────────────────────────────────────────────────────────────────

type fakeScreen struct {
	img *image.RGBA
}

func (f *fakeScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	return f.img.SubImage(rect), nil
}

func (f *fakeScreen) GrabFullHash(ctx context.Context) (uint32, error) {
	return PixelHash(f.img, nil), nil
}

func (f *fakeScreen) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := f.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return PixelHash(img, nil), nil
}

func TestFirstPixel(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	img.SetRGBA(3, 3, color.RGBA{R: 42, G: 84, B: 126, A: 255})
	sc := &fakeScreen{img: img}

	c, err := FirstPixel(context.Background(), sc, image.Rect(3, 3, 6, 6))
	if err != nil {
		t.Fatal(err)
	}
	if c.R != 42 || c.G != 84 || c.B != 126 {
		t.Fatalf("expected (42,84,126) got (%d,%d,%d)", c.R, c.G, c.B)
	}
}

// ── matchAt ───────────────────────────────────────────────────────────────────

func TestMatchAt(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 10, 10))
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))

	// Fill a 2x2 patch in src at (3,3).
	for y := range 2 {
		for x := range 2 {
			c := color.RGBA{R: uint8(x*100 + y*50), G: 0, B: 0, A: 255}
			src.SetRGBA(3+x, 3+y, c)
			ref.SetRGBA(x, y, c)
		}
	}

	if !matchAt(src, ref, 3, 3) {
		t.Fatal("should match at (3,3)")
	}
	if matchAt(src, ref, 0, 0) {
		t.Fatal("should not match at (0,0)")
	}
}

// ── LocateExact ───────────────────────────────────────────────────────────────

func TestLocateExact(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	needle := image.NewRGBA(image.Rect(0, 0, 3, 3))

	// Place a recognizable pattern at (5,7).
	for y := range 3 {
		for x := range 3 {
			c := color.RGBA{R: 200, G: uint8(x * 80), B: uint8(y * 80), A: 255}
			img.SetRGBA(5+x, 7+y, c)
			needle.SetRGBA(x, y, c)
		}
	}

	sc := &fakeScreen{img: img}
	found, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 20, 20), needle)
	if err != nil {
		t.Fatal(err)
	}
	if found.Min.X != 5 || found.Min.Y != 7 {
		t.Fatalf("expected (5,7), got (%d,%d)", found.Min.X, found.Min.Y)
	}
	if found.Dx() != 3 || found.Dy() != 3 {
		t.Fatalf("expected 3x3, got %dx%d", found.Dx(), found.Dy())
	}
}

func TestLocateExactFlatFirstRow(t *testing.T) {
	src := image.NewRGBA(image.Rect(10, 20, 26, 36))
	background := color.RGBA{R: 32, G: 48, B: 64, A: 255}
	for y := src.Rect.Min.Y; y < src.Rect.Max.Y; y++ {
		for x := src.Rect.Min.X; x < src.Rect.Max.X; x++ {
			src.SetRGBA(x, y, background)
		}
	}
	ref := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < ref.Rect.Dy(); y++ {
		for x := 0; x < ref.Rect.Dx(); x++ {
			pixel := color.RGBA{R: uint8(90 + x), G: uint8(120 + y), B: 200, A: 255}
			if y == 0 {
				pixel = background
			}
			ref.SetRGBA(x, y, pixel)
		}
	}
	for y := 0; y < ref.Rect.Dy(); y++ {
		copy(src.Pix[(22+y-src.Rect.Min.Y)*src.Stride+(14-src.Rect.Min.X)*4:], ref.Pix[y*ref.Stride:y*ref.Stride+ref.Rect.Dx()*4])
	}

	searchArea := image.Rect(100, 200, 116, 216)
	found, err := LocateExactInImage(src, searchArea, ref)
	if err != nil {
		t.Fatalf("LocateExactInImage: %v", err)
	}
	want := image.Rect(104, 202, 108, 205)
	if found != want {
		t.Fatalf("LocateExactInImage = %v, want %v", found, want)
	}
}

func TestLocateExactEmptyReference(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	sc := &fakeScreen{img: img}

	_, err := LocateExact(context.Background(), sc, image.Rect(0, 0, 20, 20), image.NewRGBA(image.Rect(0, 0, 0, 0)))
	if err == nil {
		t.Fatal("expected error for empty reference image")
	}
}

func TestPixelHashTruncatedBuffer(t *testing.T) {
	// A malformed *image.RGBA where Pix is shorter than Bounds*Stride
	malformed := &image.RGBA{
		Pix:    make([]byte, 4), // only 1 pixel worth of bytes
		Stride: 16,
		Rect:   image.Rect(0, 0, 4, 4), // needs 64 bytes
	}
	// Should not panic, falls back safely
	_ = PixelHash(malformed, nil)
}

func TestPixelHashRejectsOverflowingPackedMetadata(t *testing.T) {
	malformed := &image.RGBA{
		Pix:    make([]byte, 1),
		Stride: int(^uint(0) >> 1),
		Rect:   image.Rect(0, 0, int(^uint(0)>>1), 2),
	}
	if got := PixelHash(malformed, nil); got != 0 {
		t.Fatalf("PixelHash = %08x for overflowing packed metadata, want 0", got)
	}
}

func TestPixelFoundTruncatedBuffer(t *testing.T) {
	malformed := &image.RGBA{
		Pix:    make([]byte, 4),
		Stride: 16,
		Rect:   image.Rect(0, 0, 4, 4),
	}
	// Should not panic, returns false
	_, found := PixelFound(malformed, malformed.Rect, color.RGBA{R: 255, A: 255}, 0)
	if found {
		t.Fatal("expected not found on truncated buffer")
	}

	malformedNRGBA := &image.NRGBA{
		Pix:    make([]byte, 4),
		Stride: 16,
		Rect:   image.Rect(0, 0, 4, 4),
	}
	_, foundNRGBA := PixelFound(malformedNRGBA, malformedNRGBA.Rect, color.RGBA{R: 255, A: 255}, 0)
	if foundNRGBA {
		t.Fatal("expected not found on truncated NRGBA buffer")
	}
}

func TestLocateExactTruncatedBuffer(t *testing.T) {
	src := &image.RGBA{
		Pix:    make([]byte, 8),
		Stride: 16,
		Rect:   image.Rect(0, 0, 4, 4),
	}
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	_, err := LocateExactInImage(src, image.Rect(0, 0, 4, 4), ref)
	if err == nil {
		t.Fatal("expected error locating in truncated image buffer")
	}
}

func TestLocateExactRejectsOverflowingPackedMetadata(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	src := &image.RGBA{
		Pix:    make([]byte, 1),
		Stride: maxInt,
		Rect:   image.Rect(0, 0, maxInt, 2),
	}
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if _, err := LocateExactInImage(src, image.Rect(0, 0, 1, 1), ref); err == nil {
		t.Fatal("LocateExactInImage accepted overflowing packed metadata")
	}
}

func TestWaitFor(t *testing.T) {
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// WaitFor with a hash that doesn't exist should timeout.
	_, err := WaitFor(ctx, sc, image.Rect(0, 0, 10, 10), 0x1234, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRectangleAreaRejectsOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if _, ok := rectangleArea(image.Rect(0, 0, maxInt, maxInt)); ok {
		t.Fatal("rectangleArea accepted a product that overflows uint64")
	}
}

func TestWaitForTimeout(t *testing.T) {
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := WaitFor(ctx, sc, image.Rect(0, 0, 10, 10), 0x9999, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// TestWaitFor_Success tests the happy path where WaitFor finds the matching hash immediately.
func TestWaitFor_Success(t *testing.T) {
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get the hash of the solid color image
	img, err := sc.Grab(context.Background(), image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatalf("Grab failed: %v", err)
	}
	hash := PixelHash(img, nil)

	result, err := WaitFor(ctx, sc, image.Rect(0, 0, 10, 10), hash, 10*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WaitFor returned unexpected error: %v", err)
	}
	if result != hash {
		t.Fatalf("WaitFor returned hash %08x, want %08x", result, hash)
	}
}

// TestWaitFor_DifferentHash tests WaitFor with a hash that will never match within timeout.
func TestWaitFor_DifferentHash(t *testing.T) {
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := WaitFor(ctx, sc, image.Rect(0, 0, 10, 10), 0x99999999, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestWaitFor_WithCustomHasher tests WaitFor with a custom hasher.
func TestWaitFor_WithCustomHasher(t *testing.T) {
	customHasher := adler32.New
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	img, err := sc.Grab(context.Background(), image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatalf("Grab failed: %v", err)
	}
	hash := PixelHash(img, customHasher)

	result, err := WaitFor(ctx, sc, image.Rect(0, 0, 10, 10), hash, 10*time.Millisecond, customHasher)
	if err != nil {
		t.Fatalf("WaitFor with custom hasher returned unexpected error: %v", err)
	}
	if result != hash {
		t.Fatalf("WaitFor returned hash %08x, want %08x", result, hash)
	}
}

// TestWaitForChange_Success tests the happy path where WaitForChange detects a change.
func TestWaitForChange_Success(t *testing.T) {
	sc := &changingScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	img, err := sc.Grab(context.Background(), image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatalf("Grab failed: %v", err)
	}
	initial := PixelHash(img, nil)
	result, err := WaitForChange(ctx, sc, image.Rect(0, 0, 10, 10), initial, 10*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WaitForChange returned unexpected error: %v", err)
	}
	if result == initial {
		t.Fatalf("WaitForChange returned same hash, expected change")
	}
}

// TestWaitForChange_Timeout tests WaitForChange when no change occurs (timeout expected).
func TestWaitForChange_Timeout(t *testing.T) {
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	img, err := sc.Grab(context.Background(), image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatalf("Grab failed: %v", err)
	}
	initial := PixelHash(img, nil)
	_, err = WaitForChange(ctx, sc, image.Rect(0, 0, 10, 10), initial, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestWaitForNoChange_Success tests the happy path where WaitForNoChange detects stability.
func TestWaitForNoChange_Success(t *testing.T) {
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	img, err := sc.Grab(context.Background(), image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatalf("Grab failed: %v", err)
	}
	hash := PixelHash(img, nil)
	result, err := WaitForNoChange(ctx, sc, image.Rect(0, 0, 10, 10), 3, 10*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WaitForNoChange returned unexpected error: %v", err)
	}
	if result != hash {
		t.Fatalf("WaitForNoChange returned hash %08x, want %08x", result, hash)
	}
}

// TestWaitForNoChange_Timeout tests WaitForNoChange when changes keep happening (timeout expected).
func TestWaitForNoChange_Timeout(t *testing.T) {
	sc := &changingScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := sc.Grab(context.Background(), image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatalf("Grab failed: %v", err)
	}
	_, err = WaitForNoChange(ctx, sc, image.Rect(0, 0, 10, 10), 3, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestWaitForFn_Success tests WaitForFn with a predicate that becomes true.
func TestWaitForFn_Success(t *testing.T) {
	sc := &changingScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initialImg, err := sc.Grab(context.Background(), image.Rect(0, 0, 10, 10))
	if err != nil {
		t.Fatalf("Grab failed: %v", err)
	}
	initialHash := PixelHash(initialImg, nil)

	// Predicate that checks for a different hash
	pred := func(_ context.Context, img image.Image) bool {
		return PixelHash(img, nil) != initialHash
	}

	result, err := WaitForFn(ctx, sc, image.Rect(0, 0, 10, 10), pred, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForFn returned unexpected error: %v", err)
	}
	if PixelHash(result, nil) == initialHash {
		t.Fatalf("WaitForFn returned image with same hash, expected change")
	}
}

// TestWaitForFn_Timeout tests WaitForFn when predicate never becomes true (timeout expected).
func TestWaitForFn_Timeout(t *testing.T) {
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	pred := func(_ context.Context, img image.Image) bool {
		return false // never true
	}

	_, err := WaitForFn(ctx, sc, image.Rect(0, 0, 10, 10), pred, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestWaitForFnZeroPoll(t *testing.T) {
	sc := &solidScreenshotter{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := WaitForFn(ctx, sc, image.Rect(0, 0, 10, 10), func(_ context.Context, _ image.Image) bool {
		return false
	}, 0)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestGrabHash tests the GrabHash utility function.
func TestGrabHash_Success(t *testing.T) {
	sc := &solidScreenshotter{}
	hash, err := GrabHash(context.Background(), sc, image.Rect(0, 0, 10, 10), nil)
	if err != nil {
		t.Fatalf("GrabHash returned unexpected error: %v", err)
	}
	if hash == 0 {
		t.Fatal("GrabHash returned zero hash")
	}
}

// TestGrabHashSubImage tests that hashing a sub-image works correctly.
func TestGrabHashSubImage(t *testing.T) {
	fullImg := image.NewRGBA(image.Rect(0, 0, 10, 10))
	for y := range 10 {
		for x := range 10 {
			fullImg.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 42, A: 255})
		}
	}

	sc := &fakeScreen{img: fullImg}
	fullHash, err := GrabHash(context.Background(), sc, image.Rect(0, 0, 10, 10), nil)
	if err != nil {
		t.Fatalf("GrabHash full image failed: %v", err)
	}

	subHash, err := GrabHash(context.Background(), sc, image.Rect(2, 2, 5, 5), nil)
	if err != nil {
		t.Fatalf("GrabHash sub-image failed: %v", err)
	}

	// Build equivalent standalone image for sub-region
	equiv := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for y := range 3 {
		for x := range 3 {
			equiv.SetRGBA(x, y, fullImg.RGBAAt(x+2, y+2))
		}
	}
	equivHash := PixelHash(equiv, nil)

	if subHash != equivHash {
		t.Fatalf("sub-image hash %08x != equivalent hash %08x", subHash, equivHash)
	}
	// Full hash should differ from sub-hash (very likely)
	if fullHash == subHash {
		t.Log("WARNING: full hash equals sub-image hash (unlikely but possible)")
	}
}

func TestGrabHashWithCustomHasher(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 10), G: uint8(y * 20), B: 7, A: 255})
		}
	}

	sc := &fakeScreen{img: img}
	got, err := GrabHash(context.Background(), sc, image.Rect(0, 0, 4, 4), adler32.New)
	if err != nil {
		t.Fatalf("GrabHash returned error: %v", err)
	}
	want := PixelHash(img, adler32.New)
	if got != want {
		t.Fatalf("GrabHash with custom hasher = %08x, want %08x", got, want)
	}
}

// TestScanFor tests ScanFor across multiple regions.
func TestScanFor(t *testing.T) {
	// Create an image with patterns at different locations
	img := image.NewRGBA(image.Rect(0, 0, 30, 30))
	for y := range 30 {
		for x := range 30 {
			img.SetRGBA(x, y, color.RGBA{R: 100, G: 100, B: 100, A: 255})
		}
	}

	// Place a unique pattern at (5,5) and (15,15)
	for y := range 2 {
		for x := range 2 {
			img.SetRGBA(5+x, 5+y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
			img.SetRGBA(15+x, 15+y, color.RGBA{R: 0, G: 255, B: 0, A: 255})
		}
	}

	sc := &fakeScreen{img: img}
	rects := []image.Rectangle{
		image.Rect(0, 0, 10, 10),
		image.Rect(10, 10, 20, 20),
	}
	// Compute expected hashes from the image subregions so ScanFor can match them.
	wants := make([]uint32, len(rects))
	for i, r := range rects {
		sub := img.SubImage(r)
		wants[i] = PixelHash(sub, nil)
	}

	// Diagnostic: inspect the union bbox grab to ensure the grabbed image
	// is RGBA-backed and that SubImage yields *image.RGBA instances. If
	// this assumption fails the test will fail fast with diagnostic output.
	bbox := rects[0]
	for _, r := range rects {
		bbox = bbox.Union(r)
	}
	grabbed, err := sc.Grab(context.Background(), bbox)
	if err != nil {
		t.Fatalf("grab failed: %v", err)
	}

	subIf, ok := grabbed.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		t.Fatalf("grabbed image does not support SubImage: type=%T", grabbed)
	}
	for _, r := range rects {
		tr := image.Rect(
			r.Min.X-bbox.Min.X,
			r.Min.Y-bbox.Min.Y,
			r.Max.X-bbox.Min.X,
			r.Max.Y-bbox.Min.Y,
		)
		subImg := subIf.SubImage(tr)
		if _, ok := subImg.(*image.RGBA); !ok {
			t.Fatalf("subimage is not *image.RGBA (type=%T); test requires RGBA-backed image", subImg)
		}
	}

	// Now run ScanFor (the real call under test).
	result, err := ScanFor(context.Background(), sc, rects, wants, 1*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("ScanFor returned error: %v", err)
	}

	// Verify one of the matches was found
	foundMatch := false
	for _, r := range rects {
		if result.Rect.Eq(r) {
			foundMatch = true
			break
		}
	}
	if !foundMatch {
		t.Fatalf("ScanFor did not find expected match in any of the search regions")
	}
}

func TestScanForWithCustomHasher(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 12, 6))
	for y := range 6 {
		for x := range 12 {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x + 1), G: uint8(y + 1), A: 255})
		}
	}

	rects := []image.Rectangle{image.Rect(0, 0, 4, 6), image.Rect(8, 0, 12, 6)}
	wantSecond := PixelHash(img.SubImage(rects[1]), adler32.New)
	wantFirst := PixelHash(img.SubImage(rects[0]), adler32.New) ^ 1

	result, err := ScanFor(context.Background(), &fakeScreen{img: img}, rects, []uint32{wantFirst, wantSecond}, time.Millisecond, adler32.New)
	if err != nil {
		t.Fatalf("ScanFor with custom hasher returned error: %v", err)
	}
	if result.Rect != rects[1] || result.Hash != wantSecond {
		t.Fatalf("ScanFor with custom hasher returned %+v, want rect %v and hash %08x", result, rects[1], wantSecond)
	}
}

func TestScanForEmpty(t *testing.T) {
	_, err := ScanFor(context.Background(), &solidScreenshotter{}, nil, nil, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected error for empty ScanFor input")
	}

	_, err = ScanFor(context.Background(), &solidScreenshotter{}, []image.Rectangle{image.Rect(0, 0, 0, 1)}, []uint32{1}, 10*time.Millisecond, nil)
	if err == nil {
		t.Fatal("expected error for empty ScanFor region")
	}
}

// TestFindColor tests FindColor function.
func TestFindColor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// Fill with gray
	for y := range 10 {
		for x := range 10 {
			img.SetRGBA(x, y, color.RGBA{R: 100, G: 100, B: 100, A: 255})
		}
	}
	// Place a red pixel at (3,3)
	img.SetRGBA(3, 3, color.RGBA{R: 255, G: 0, B: 0, A: 255})

	sc := &fakeScreen{img: img}
	target := color.RGBA{R: 255, G: 0, B: 0, A: 255}

	pt, err := FindColor(context.Background(), sc, image.Rect(0, 0, 10, 10), target, 10)
	if err != nil {
		t.Fatalf("FindColor returned error: %v", err)
	}
	if pt.X != 3 || pt.Y != 3 {
		t.Fatalf("FindColor returned (%d,%d), want (3,3)", pt.X, pt.Y)
	}

	// Test with tolerance
	target2 := color.RGBA{R: 250, G: 5, B: 5, A: 255} // close to red
	pt2, err := FindColor(context.Background(), sc, image.Rect(0, 0, 10, 10), target2, 10)
	if err != nil {
		t.Fatalf("FindColor with tolerance returned error: %v", err)
	}
	if pt2.X != 3 || pt2.Y != 3 {
		t.Fatalf("FindColor with tolerance returned (%d,%d), want (3,3)", pt2.X, pt2.Y)
	}

	if _, err := FindColor(context.Background(), sc, image.Rect(0, 0, 10, 10), target, -1); err == nil {
		t.Fatal("expected error for negative tolerance")
	}
}

func TestPixelFoundPackedImages(t *testing.T) {
	const (
		minX = 10
		minY = 20
	)
	bounds := image.Rect(minX, minY, minX+4, minY+4)
	wantColor := color.RGBA{R: 100, G: 120, B: 140, A: 255}

	rgba := image.NewRGBA(bounds)
	rgba.SetRGBA(12, 22, wantColor)
	nrgba := image.NewNRGBA(bounds)
	nrgba.SetNRGBA(12, 22, color.NRGBA(wantColor))

	tests := []struct {
		name      string
		img       image.Image
		target    color.RGBA
		tolerance int
		want      image.Point
		wantFound bool
	}{
		{
			name:      "rgba exact",
			img:       rgba,
			target:    wantColor,
			want:      image.Pt(12, 22),
			wantFound: true,
		},
		{
			name:      "nrgba within tolerance",
			img:       nrgba,
			target:    color.RGBA{R: 99, G: 119, B: 139, A: 255},
			tolerance: 1,
			want:      image.Pt(12, 22),
			wantFound: true,
		},
		{
			name:      "packed pixel absent",
			img:       rgba,
			target:    color.RGBA{R: 1, G: 2, B: 3, A: 255},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := PixelFound(tt.img, bounds, tt.target, tt.tolerance)
			if found != tt.wantFound {
				t.Fatalf("PixelFound found = %t, want %t", found, tt.wantFound)
			}
			if found && got != tt.want {
				t.Fatalf("PixelFound point = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPixelFoundDoesNotScanOutsideRequestedRect(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	want := color.RGBA{R: 255, A: 255}
	img.SetRGBA(3, 3, want)

	if _, ok := PixelFound(img, image.Rect(0, 0, 2, 2), want, 0); ok {
		t.Fatal("PixelFound found a color outside the requested rectangle")
	}
}

// TestWaitForLocate tests WaitForLocate.
func TestWaitForLocate(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	needle := image.NewRGBA(image.Rect(0, 0, 3, 3))

	for y := range 3 {
		for x := range 3 {
			c := color.RGBA{R: 200, G: uint8(x * 80), B: uint8(y * 80), A: 255}
			img.SetRGBA(5+x, 7+y, c)
			needle.SetRGBA(x, y, c)
		}
	}

	sc := &fakeScreen{img: img}
	found, err := WaitForLocate(context.Background(), sc, image.Rect(0, 0, 20, 20), needle, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForLocate returned error: %v", err)
	}
	if found.Min.X != 5 || found.Min.Y != 7 {
		t.Fatalf("WaitForLocate returned (%d,%d), want (5,7)", found.Min.X, found.Min.Y)
	}
}

func TestWaitForLocateZeroPoll(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := range 20 {
		for x := range 20 {
			img.SetRGBA(x, y, color.RGBA{R: 100, G: 100, B: 100, A: 255})
		}
	}
	needle := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for y := range 3 {
		for x := range 3 {
			needle.SetRGBA(x, y, color.RGBA{R: 200, G: 0, B: 0, A: 255})
		}
	}

	sc := &fakeScreen{img: img}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := WaitForLocate(ctx, sc, image.Rect(0, 0, 20, 20), needle, 0)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// solidScreenshotter returns a solid color image.

type solidScreenshotter struct{}

func (s *solidScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	img := image.NewRGBA(rect)
	c := color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img, nil
}

func (s *solidScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	img, _ := s.Grab(ctx, image.Rect(0, 0, 100, 100))
	return PixelHash(img, nil), nil
}

func (s *solidScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return PixelHash(img, nil), nil
}

// changingScreenshotter alternates between two colors.

type changingScreenshotter struct {
	count int
}

func (c *changingScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	img := image.NewRGBA(rect)
	if c.count%2 == 0 {
		col := color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				img.SetRGBA(x, y, col)
			}
		}
	} else {
		col := color.RGBA{R: 0x78, G: 0x9a, B: 0xbc, A: 0xff}
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				img.SetRGBA(x, y, col)
			}
		}
	}
	c.count++
	return img, nil
}

func (c *changingScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	img, _ := c.Grab(ctx, image.Rect(0, 0, 100, 100))
	return PixelHash(img, nil), nil
}

func (c *changingScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := c.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return PixelHash(img, nil), nil
}

// Hashing is an observation operation. A canceled caller must not authorize a
// framebuffer read, regardless of whether the backend provides a canonical
// zero-copy path or an ordinary image grab.
func TestGrabHashNeverTouchesBackendAfterCallerPreCanceled(t *testing.T) {
	rect := image.Rect(1, 1, 3, 3)
	for _, tc := range []struct {
		name      string
		rect      image.Rectangle
		canonical bool
	}{
		{"canonical region", rect, true},
		{"canonical full screen", image.Rectangle{}, true},
		{"regular image grab", rect, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if tc.canonical {
				capture := &canonicalHashScreenshotter{img: testHashImageRGBA()}
				h, err := GrabHash(ctx, capture, tc.rect, nil)
				if !errors.Is(err, context.Canceled) || h != 0 || capture.grabCalls != 0 || capture.regionCalls != 0 || capture.fullCalls != 0 {
					t.Fatalf("canceled hash accessed framebuffer: hash=%08x err=%v grabs=%d region=%d full=%d", h, err, capture.grabCalls, capture.regionCalls, capture.fullCalls)
				}
			} else {
				capture := &pixelInspectionScreenshotter{img: testHashImageRGBA()}
				h, err := GrabHash(ctx, capture, tc.rect, nil)
				if !errors.Is(err, context.Canceled) || h != 0 || capture.grabs != 0 {
					t.Fatalf("canceled hash captured pixels: hash=%08x err=%v grabs=%d", h, err, capture.grabs)
				}
			}
		})
	}
}

// An invalid rectangle or previously canceled hash operation must not
// trigger a capture. A nonempty rect cannot begin at MaxInt: there is no
// representable exclusive Max.X or Max.Y, so the existing Empty check is the
// only reachable preflight for such input.
func TestFindDirectHashAndPixelPreflightRejectBeforeCapture(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name string
		rect image.Rectangle
	}{
		{name: "x coordinate overflow", rect: image.Rectangle{Min: image.Pt(maxInt, 0), Max: image.Pt(maxInt, 1)}},
		{name: "y coordinate overflow", rect: image.Rectangle{Min: image.Pt(0, maxInt), Max: image.Pt(1, maxInt)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &pixelInspectionScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 1, 1))}
			// Construct a nonempty rectangle without overflowing its coordinate;
			// an inverse edge is permitted at this trust boundary and the overflow
			// preflight must take priority over any attempt to grab the backend.
			rect := tc.rect
			if rect.Min.X == maxInt {
				rect.Max.X = maxInt
			} else {
				rect.Max.Y = maxInt
			}
			p, err := FirstPixel(context.Background(), backend, rect)
			if err == nil || p != (struct{ R, G, B, A uint8 }{}) || backend.grabs != 0 {
				t.Fatalf("unsafe pixel coordinate reached backend: pixel=%+v err=%v grabs=%d", p, err, backend.grabs)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &pixelInspectionScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 2, 2))}
	hash, err := grabImageHash(ctx, backend, image.Rect(0, 0, 2, 2), nil, false)
	if hash != 0 || !errors.Is(err, context.Canceled) || backend.grabs != 0 {
		t.Fatalf("pre-canceled direct image hash accessed capture: hash=%08x err=%v grabs=%d", hash, err, backend.grabs)
	}
}

type cancelingCanonicalHashBackend struct {
	cancel       context.CancelFunc
	full, region int
}

func (b *cancelingCanonicalHashBackend) Grab(context.Context, image.Rectangle) (image.Image, error) {
	panic("canonical hash backend must not Grab")
}
func (b *cancelingCanonicalHashBackend) CanonicalHashing() bool { return true }
func (b *cancelingCanonicalHashBackend) GrabFullHash(context.Context) (uint32, error) {
	b.full++
	b.cancel()
	return 0x12345678, nil
}
func (b *cancelingCanonicalHashBackend) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	b.region++
	b.cancel()
	return 0x12345678, nil
}

func TestGrabHashCannotReturnSuccessAfterCancellationInsideCapture(t *testing.T) {
	rect := image.Rect(0, 0, 4, 4)
	for _, tc := range []struct {
		name string
		run  func(context.Context, context.CancelFunc) (uint32, error)
	}{
		{"image conversion", func(ctx context.Context, cancel context.CancelFunc) (uint32, error) {
			image := &cancelOnPixelImage{bounds: rect, cancel: cancel}
			return GrabHash(ctx, &pixelInspectionScreenshotter{img: image}, rect, nil)
		}},
		{"canonical region", func(ctx context.Context, cancel context.CancelFunc) (uint32, error) {
			b := &cancelingCanonicalHashBackend{cancel: cancel}
			h, err := GrabHash(ctx, b, rect, nil)
			if b.region != 1 || b.full != 0 {
				t.Errorf("wrong canonical path region=%d full=%d", b.region, b.full)
			}
			return h, err
		}},
		{"canonical full", func(ctx context.Context, cancel context.CancelFunc) (uint32, error) {
			b := &cancelingCanonicalHashBackend{cancel: cancel}
			h, err := GrabHash(ctx, b, image.Rectangle{}, nil)
			if b.region != 0 || b.full != 1 {
				t.Errorf("wrong canonical path region=%d full=%d", b.region, b.full)
			}
			return h, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hash, err := tc.run(ctx, cancel)
			if !errors.Is(err, context.Canceled) || hash != 0 {
				t.Fatalf("canceled private hash observation published: hash=%08x err=%v", hash, err)
			}
		})
	}
}

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

// Anchor recovery must score absolute differences instead of signed channel
// subtraction. A darker, distinctive leading pixel in an otherwise similar row must
// be selected ahead of lower-contrast windows after a candidate collision.
func TestLocateAnchorSelectsHighContrastDarkerReferenceWindow(t *testing.T) {
	bounds := image.Rect(0, 0, 5, 1)
	ref := image.NewRGBA(bounds)
	for x := 1; x < 5; x++ {
		ref.SetRGBA(x, 0, color.RGBA{R: 230, G: 230, B: 230, A: 255})
	}
	ref.SetRGBA(0, 0, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	sourcePixel := []byte{240, 240, 240, 255}
	row, start, length := selectLocateAnchor(ref, bounds, bounds.Dx()*4, sourcePixel)
	if row != 0 || start != 1 || length != locateAnchorWindowBytes {
		t.Fatalf("dark contrast window not preferred: row=%d start=%d length=%d", row, start, length)
	}
}

type malformedCaptureBackend struct{ frame image.Image }

func (b malformedCaptureBackend) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return b.frame, nil
}
func (b malformedCaptureBackend) GrabFullHash(context.Context) (uint32, error) { return 0, nil }
func (b malformedCaptureBackend) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	return 0, nil
}
func (b malformedCaptureBackend) Close() error { return nil }

func TestUntrustedTruncatedCaptureNeverPanicsPixelInspection(t *testing.T) {
	rgba := &image.RGBA{Pix: []byte{1, 2}, Stride: 4, Rect: image.Rect(0, 0, 1, 1)}
	nrgba := &image.NRGBA{Pix: []byte{1, 2}, Stride: 4, Rect: image.Rect(0, 0, 1, 1)}
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{{"RGBA", rgba}, {"NRGBA", nrgba}} {
		t.Run(tc.name, func(t *testing.T) {
			backend := malformedCaptureBackend{frame: tc.frame}
			if pixel, err := FirstPixel(context.Background(), backend, image.Rect(0, 0, 1, 1)); !errors.Is(err, ErrInvalidFrame) || pixel != (color.RGBA{}) {
				t.Fatalf("FirstPixel accepted truncated %s: %v %v", tc.name, pixel, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := WaitForNoChangeFrom(ctx, backend, image.Rect(0, 0, 1, 1), 0, 2, time.Millisecond, nil); !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("settle loop accepted truncated %s: %v", tc.name, err)
			}
			if hash := PixelHash(tc.frame, nil); hash != 0 {
				t.Fatalf("truncated %s image hashed nonzero: %08x", tc.name, hash)
			}
		})
	}
}

// Capture clients must distinguish a corrupted backend frame from an actual
// matching hash of zero, including optimized compact scans and sparse scans.
// An invalid NRGBA layout can panic during draw.Draw without validation.
func TestMalformedCapturedFramesCannotSatisfyHashOrScan(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{
		{"truncated RGBA", &image.RGBA{Pix: []byte{2}, Stride: 8, Rect: image.Rect(0, 0, 2, 2)}},
		{"truncated NRGBA", &image.NRGBA{Pix: []byte{2}, Stride: 8, Rect: image.Rect(0, 0, 2, 2)}},
		{"empty RGBA", image.NewRGBA(image.Rectangle{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := malformedCaptureBackend{frame: tc.frame}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			for _, op := range []struct {
				name string
				run  func() error
			}{
				{"GrabHash", func() error { _, err := GrabHash(ctx, sc, image.Rect(0, 0, 2, 2), nil); return err }},
				{"WaitFor zero", func() error { _, err := WaitFor(ctx, sc, image.Rect(0, 0, 2, 2), 0, 0, nil); return err }},
				{"Scan compact zero", func() error {
					_, err := ScanFor(ctx, sc, []image.Rectangle{image.Rect(0, 0, 1, 2), image.Rect(1, 0, 2, 2)}, []uint32{0, 0}, 0, nil)
					return err
				}},
				{"Scan sparse zero", func() error {
					_, err := ScanFor(ctx, sc, []image.Rectangle{image.Rect(0, 0, 1, 1), image.Rect(500, 500, 501, 501)}, []uint32{0, 0}, 0, nil)
					return err
				}},
			} {
				t.Run(op.name, func(t *testing.T) {
					if err := op.run(); !errors.Is(err, ErrInvalidFrame) {
						t.Fatalf("invalid captured frame not rejected: %v", err)
					}
				})
			}
		})
	}
}

// NRGBA stores straight-alpha channels, but color.RGBAModel and public
// PixelFound targets are premultiplied RGBA. All supported capture image types
// must agree on whether a translucent pixel matches a requested color.
func TestPixelFoundNRGBAAlphaUsesCanonicalRGBAColorAcrossBackends(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 3, 3))
	source.SetNRGBA(2, 1, color.NRGBA{R: 255, G: 75, B: 15, A: 64})
	target := color.RGBAModel.Convert(source.NRGBAAt(2, 1)).(color.RGBA) //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
	wrong := color.RGBA{R: 255, G: 75, B: 15, A: 255}
	for _, tc := range []struct {
		name  string
		image image.Image
	}{
		{"native NRGBA", source},
		{"generic image adapter", syntheticImage{source}},
		{"canonical RGBA copy", func() image.Image {
			converted := image.NewRGBA(source.Bounds())
			for y := 0; y < 3; y++ {
				for x := 0; x < 3; x++ {
					converted.Set(x, y, source.At(x, y))
				}
			}
			return converted
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pixel, found := PixelFound(tc.image, tc.image.Bounds(), target, 0)
			if !found || pixel != (image.Pt(2, 1)) {
				t.Fatalf("translucent pixel not matched by canonical RGBA: found=%v pixel=%v target=%v", found, pixel, target)
			}
			if pixel, found := PixelFound(tc.image, tc.image.Bounds(), wrong, 0); found {
				t.Fatalf("straight-alpha color falsely matched premultiplied target: %v", pixel)
			}
			point, err := FindColor(context.Background(), malformedCaptureBackend{frame: tc.image}, tc.image.Bounds(), target, 0)
			if err != nil || point != (image.Pt(2, 1)) {
				t.Fatalf("FindColor translucent result=%v error=%v", point, err)
			}
			_, err = FindColor(context.Background(), malformedCaptureBackend{frame: tc.image}, tc.image.Bounds(), wrong, 0)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("FindColor incorrectly matched straight-alpha bytes: %v", err)
			}
		})
	}
}

func TestBGRAFrameValidationRejectsOverflowAndTruncationWithoutPanics(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		data                  []byte
		width, height, stride int
	}{
		{"empty", nil, 1, 1, 4},
		{"negative width", make([]byte, 4), -1, 1, 4},
		{"zero height", make([]byte, 4), 1, 0, 4},
		{"zero stride", make([]byte, 4), 1, 1, 0},
		{"width multiplied by four overflows", make([]byte, 4), math.MaxInt, 1, math.MaxInt},
		{"stride smaller than packed row", make([]byte, 8), 2, 1, 4},
		{"total rows overflow", make([]byte, 16), 1, math.MaxInt, math.MaxInt},
		{"truncated last row", make([]byte, 11), 1, 3, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := bgraFrameBounds(tc.data, tc.width, tc.height, tc.stride); ok || !got.Empty() {
				t.Fatalf("invalid layout accepted: %+v", got)
			}
			if hash, err := PixelHashBGRA(tc.data, tc.width, tc.height, tc.stride, image.Rectangle{}); hash != 0 || !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("invalid frame hash=%08x err=%v", hash, err)
			}
		})
	}
	data, _, stride := syntheticBGRAFrame(3, 2, 5, 19)
	for _, rect := range []image.Rectangle{image.Rect(4, 0, 5, 1), image.Rect(0, 3, 1, 4), image.Rect(-9, -3, -1, -1)} {
		if hash, err := PixelHashBGRA(data, 3, 2, stride, rect); hash != 0 || !errors.Is(err, ErrEmptyRegion) {
			t.Fatalf("off-screen %v hash=%08x err=%v", rect, hash, err)
		}
	}
}

func TestPackedImageLayoutAndPixelScanRejectMalformedBuffers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rect   image.Rectangle
		stride int
		bytes  int
	}{
		{"empty", image.Rectangle{}, 0, 0},
		{"negative stride", image.Rect(0, 0, 2, 2), -4, 16},
		{"short row", image.Rect(0, 0, 2, 2), 4, 16},
		{"row-count overflow", image.Rect(0, 0, 2, math.MaxInt), math.MaxInt, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if n, ok := packedBufferSize(tc.rect, tc.stride); ok || n != 0 {
				t.Fatalf("invalid packed layout size=%d ok=%v", n, ok)
			}
		})
	}
	malformed := &image.RGBA{Pix: make([]byte, 3), Stride: 8, Rect: image.Rect(20, 40, 22, 42)}
	if point, ok := PixelFound(malformed, image.Rectangle{}, color.RGBA{R: 255, A: 255}, 0); ok || point != (image.Point{}) {
		t.Fatalf("truncated RGBA returned %+v %v", point, ok)
	}
	ref := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if found := matchAt(malformed, ref, 20, 40); found {
		t.Fatal("truncated source matched full image")
	}
	// The source layout is valid, but the reference has a matching stride with
	// too few bytes for even one pixel. A reported match would be untrusted.
	source := image.NewRGBA(image.Rect(20, 40, 22, 42))
	shortReference := &image.RGBA{Pix: make([]byte, 2), Stride: 4, Rect: image.Rect(0, 0, 1, 1)}
	if found := matchAt(source, shortReference, 20, 40); found {
		t.Fatal("truncated reference matched valid image")
	}
	if _, err := LocateExactInImage(source, source.Bounds(), shortReference); err == nil {
		t.Fatal("truncated reference was accepted for exact image lookup")
	}
}

func TestPixelFoundZeroOriginCaptureRestrictsToRequestedExtent(t *testing.T) {
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	target := color.RGBA{R: 230, G: 5, B: 10, A: 255}
	canvas.SetRGBA(3, 3, target)
	rect := image.Rect(50, 60, 52, 62)
	if p, ok := PixelFound(canvas, rect, target, 0); ok || p != (image.Point{}) {
		t.Fatalf("search escaped requested extent: p=%v found=%v", p, ok)
	}
	canvas.SetRGBA(1, 1, target)
	if p, ok := PixelFound(canvas, rect, target, 0); !ok || p != (image.Pt(51, 61)) {
		t.Fatalf("zero-origin translated p=%v found=%v", p, ok)
	}
}

func TestRectangleAreaRejectsIntegerOverflow(t *testing.T) {
	if area, ok := rectangleArea(image.Rectangle{}); area != 0 || ok {
		t.Fatalf("empty geometry area=%d ok=%v", area, ok)
	}
	if area, ok := rectangleArea(image.Rect(0, 0, math.MaxInt, math.MaxInt)); area != 0 || ok {
		t.Fatalf("overflow geometry area=%d ok=%v", area, ok)
	}
	if area, ok := rectangleArea(image.Rect(0, 0, 2, 3)); area != 6 || !ok {
		t.Fatalf("normal geometry area=%d ok=%v", area, ok)
	}
}

// Exact matching may receive untrusted RGBA planes with legal bounds but
// impossible buffer strides. Never fall through to a pixel comparison, which
// can panic or turn an incomplete screenshot into a false-positive match.
func TestExactPackedMatcherRejectsMalformedSourceAndReferenceLayout(t *testing.T) {
	bounds := image.Rect(0, 0, 2, 2)
	good := image.NewRGBA(bounds)
	good.SetRGBA(0, 0, color.RGBA{R: 19, A: 255})
	malformedSource := &image.RGBA{Pix: make([]byte, 16), Stride: 3, Rect: bounds}
	malformedReference := &image.RGBA{Pix: make([]byte, 16), Stride: 3, Rect: bounds}
	if matchAt(malformedSource, good, 0, 0) {
		t.Fatal("invalid capture stride accepted")
	}
	if matchAt(good, malformedReference, 0, 0) {
		t.Fatal("invalid reference stride accepted")
	}
	if !matchAt(good, good, 0, 0) {
		t.Fatal("valid packed match rejected")
	}
}

type genericEmptyFrameScreenshotter struct{ emptyImageScreenshotter }

func (genericEmptyFrameScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return image.NewGray(image.Rectangle{}), nil
}

// A compositor that sends an empty frame after a successful Grab cannot
// count that observation toward a no-change stability streak.
func TestNoChangeProbeRejectsEmptyGrabWithoutAdvancingStreak(t *testing.T) {
	streak := newStableStreak(0, 2)
	probe := grabHashProbe(context.Background(), genericEmptyFrameScreenshotter{}, image.Rect(0, 0, 2, 2), nil, &streak)
	done, hash, err := probe(0)
	if done || hash != 0 || err == nil || !strings.Contains(err.Error(), "empty image") {
		t.Fatalf("empty grab counted as stable: done=%v hash=%d err=%v", done, hash, err)
	}
}

var pixelHashContractSink uint32

func TestPixelHashDirectCRC32MatchesExplicitHasher(t *testing.T) {
	full := image.NewRGBA(image.Rect(11, 19, 139, 83))
	for y := full.Rect.Min.Y; y < full.Rect.Max.Y; y++ {
		for x := full.Rect.Min.X; x < full.Rect.Max.X; x++ {
			full.SetRGBA(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 7), B: uint8(x ^ y), A: 255})
		}
	}
	nrgba := image.NewNRGBA(full.Bounds())
	for y := nrgba.Rect.Min.Y; y < nrgba.Rect.Max.Y; y++ {
		for x := nrgba.Rect.Min.X; x < nrgba.Rect.Max.X; x++ {
			nrgba.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 21, A: uint8(32 + (x+y)%200)})
		}
	}
	cases := []struct {
		name string
		img  image.Image
	}{
		{"full RGBA", full},
		{"padded RGBA subimage", full.SubImage(image.Rect(25, 30, 72, 68))},
		{"full-width subimage", full.SubImage(image.Rect(11, 36, 139, 57))},
		{"nonopaque NRGBA", nrgba},
		{"subregion NRGBA", nrgba.SubImage(image.Rect(24, 27, 85, 61))},
		{"gray", image.NewGray(image.Rect(-5, -6, 12, 18))},
		{"empty RGBA", image.NewRGBA(image.Rectangle{})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PixelHash(tc.img, nil)
			want := PixelHash(tc.img, crc32.NewIEEE)
			if got != want {
				t.Fatalf("canonical hash=%08x explicit CRC32=%08x", got, want)
			}
			custom := PixelHash(tc.img, adler32.New)
			if custom == got && tc.img.Bounds().Dx() > 0 && tc.img.Bounds().Dy() > 0 {
				t.Fatalf("custom Adler32 unexpectedly identical to CRC32: %08x", custom)
			}
		})
	}
}
func TestPixelHashDefaultRGBANoHeapAllocation(t *testing.T) {
	full := image.NewRGBA(image.Rect(0, 0, 64, 64))
	subset := full.SubImage(image.Rect(7, 7, 47, 45))
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{"full", full}, {"subregion", subset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(100, func() { pixelHashContractSink = PixelHash(tc.img, nil) })
			if allocs != 0 {
				t.Fatalf("default RGBA hash allocations = %g, want 0", allocs)
			}
		})
	}
}

// DefaultHasher is an assignable public variable, not a fixed alias. A custom
// default must keep working when callers request a nil hash factory.
func TestPixelHashRespectsDefaultHasherOverride(t *testing.T) {
	original := DefaultHasher
	DefaultHasher = adler32.New
	t.Cleanup(func() { DefaultHasher = original })
	img := image.NewRGBA(image.Rect(0, 0, 19, 13))
	for y := 0; y < 13; y++ {
		for x := 0; x < 19; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 19), B: 57, A: 255})
		}
	}
	got := PixelHash(img, nil)
	want := PixelHash(img, adler32.New)
	if got != want {
		t.Fatalf("modified default hash=%08x, explicit Adler32=%08x", got, want)
	}
	if got == PixelHash(img, crc32.NewIEEE) {
		t.Fatal("mutable default override was ignored")
	}
}

// A screenshotter can expose an RGBA image with valid rectangle/stride
// metadata whose pixel allocation was truncated. Neither the optimized CRC
// path nor a caller-supplied hasher may read that buffer or hash a prefix as
// though it were a complete frame.
func TestPixelHashRejectsTruncatedRGBARowsWithDefaultAndCustomHash(t *testing.T) {
	for _, tc := range []struct {
		name   string
		image  *image.RGBA
		region image.Rectangle
	}{
		{name: "truncated first scanline", image: &image.RGBA{Pix: make([]byte, 7), Stride: 8, Rect: image.Rect(10, 20, 12, 22)}, region: image.Rect(10, 20, 12, 22)},
		{name: "truncated second scanline", image: &image.RGBA{Pix: make([]byte, 12), Stride: 8, Rect: image.Rect(10, 20, 12, 22)}, region: image.Rect(10, 20, 12, 22)},
		{name: "truncated subimage final row", image: &image.RGBA{Pix: make([]byte, 27), Stride: 16, Rect: image.Rect(10, 20, 13, 22)}, region: image.Rect(11, 20, 13, 22)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, hasher := range []struct {
				name string
				new  Hasher
			}{{name: "crc32 default"}, {name: "custom adler32", new: adler32.New}} {
				t.Run(hasher.name, func(t *testing.T) {
					if got := pixelHashImage(tc.image, tc.region, hasher.new); got != 0 {
						t.Fatalf("truncated frame hashed as %08x", got)
					}
				})
			}
		})
	}
}

// Immediate fixed-interval completion must not set up a ticker that will
// never be read. This is the common early-exit path after screen hash checks.
func TestFixedPollImmediateDoesNotAllocateTicker(t *testing.T) {
	ctx := context.Background()
	immediate := func(int) (bool, uint32, error) { return true, 0x12345678, nil }
	allocs := testing.AllocsPerRun(100, func() {
		got, err := poll(ctx, time.Millisecond, 0, immediate)
		if err != nil || got != 0x12345678 {
			t.Fatalf("poll got=%08x err=%v", got, err)
		}
	})
	if allocs != 0 {
		t.Fatalf("immediate polling allocs=%g; a ticker should not be allocated", allocs)
	}
}

func TestFixedPollDelayedObservationAndCancellation(t *testing.T) {
	t.Run("delayed result", func(t *testing.T) {
		attempts := 0
		got, err := poll(context.Background(), time.Microsecond, 0, func(int) (bool, uint32, error) {
			attempts++
			return attempts == 3, uint32(attempts), nil
		})
		if err != nil || got != 3 || attempts != 3 {
			t.Fatalf("poll got=%d attempts=%d err=%v", got, attempts, err)
		}
	})
	t.Run("cancellation after first observation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		attempts := 0
		got, err := poll(ctx, time.Second, 9, func(int) (bool, uint32, error) {
			attempts++
			cancel()
			return false, 27, nil
		})
		if !errors.Is(err, context.Canceled) || got != 27 || attempts != 1 {
			t.Fatalf("poll got=%d attempts=%d err=%v", got, attempts, err)
		}
	})
}

func TestReviewOCRExactWhitespaceRejectedBeforeCapture(t *testing.T) {
	for _, mode := range []TextMatchMode{TextMatchExact, TextMatchNormalizeCase} {
		for _, query := range []string{"two  words", " leading", "trailing ", "two\twords"} {
			t.Run(string(mode)+"/"+query, func(t *testing.T) {
				area := image.Rect(0, 0, 10, 10)
				backend := &textTestCapture{capture: VerifiedTemplateCapture{Image: image.NewNRGBA(area), DesktopRect: area, PixelScale: 1}}
				provider := &textTestRecognizer{}
				_, err := RecognizeText(context.Background(), backend, area, query, TextOptions{Recognizer: provider, MatchMode: mode})
				if !errors.Is(err, ErrInvalidFrame) || backend.calls != 0 || provider.calls != 0 {
					t.Fatalf("err=%v capture=%d recognition=%d; invalid query must fail before side effects", err, backend.calls, provider.calls)
				}
			})
		}
	}
}

func TestReviewTesseractLiteralQuotes(t *testing.T) {
	for _, word := range []string{`"hello"`, `5"wide`, `"unclosed`, `"`} {
		t.Run(word, func(t *testing.T) {
			data := validTesseractHeader + "5\t1\t1\t1\t1\t1\t0\t0\t5\t5\t90\t" + word + "\n"
			got, err := parseTesseractTSV([]byte(data), 10, 10)
			if err != nil || len(got) != 1 || got[0].Text != word {
				t.Fatalf("word=%q got=%+v err=%v", word, got, err)
			}
		})
	}
}

func TestReviewTesseractTSVCRLF(t *testing.T) {
	data := strings.ReplaceAll(validTesseractHeader, "\n", "\r\n") +
		"5\t1\t1\t1\t1\t1\t0\t0\t5\t5\t90\t\"hello\"\r\n"
	got, err := parseTesseractTSV([]byte(data), 10, 10)
	if err != nil || len(got) != 1 || got[0].Text != `"hello"` {
		t.Fatalf("CRLF TSV got=%+v err=%v", got, err)
	}
}

// A custom hash provider can revoke the scan's caller during hashing. The
// combined-region fast path must return that cancellation before checking the
// hash or evaluating a later watched region.
type revokingScanHasher struct {
	hash.Hash32
	cancel context.CancelFunc
}

func (h *revokingScanHasher) Write(p []byte) (int, error) {
	n, err := h.Hash32.Write(p)
	h.cancel()
	return n, err
}

func TestScanForCompactHashRevocationCannotAcknowledgeOrHashAnotherRegion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &pixelInspectionScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 2, 2))}
	regions := []image.Rectangle{image.Rect(0, 0, 1, 2), image.Rect(1, 0, 2, 2)}
	newHash := func() hash.Hash32 { return &revokingScanHasher{Hash32: crc32.NewIEEE(), cancel: cancel} }
	got, err := ScanFor(ctx, source, regions, []uint32{0, 0}, 0, newHash)
	if !errors.Is(err, context.Canceled) || got != (Result{}) || source.grabs != 1 {
		t.Fatalf("post-hash cancellation acknowledged or continued: got=%+v err=%v grabs=%d", got, err, source.grabs)
	}
}

// ScanFor validates input before it enters the polling loop. A caller that
// revokes authority exactly at the first observation check must prevent even
// the first grab in both compact and spatially separated region modes.
type cancelOnFirstScanCheck struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelOnFirstScanCheck) Err() error {
	c.checks++
	if c.checks == 1 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestScanForRejectsRevocationBeforeFirstCapture(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rects []image.Rectangle
	}{
		{name: "compact regions", rects: []image.Rectangle{image.Rect(0, 0, 2, 2), image.Rect(2, 0, 4, 2)}},
		{name: "separated regions", rects: []image.Rectangle{image.Rect(0, 0, 2, 2), image.Rect(100, 100, 102, 102)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checked := &cancelOnFirstScanCheck{Context: ctx, cancel: cancel}
			source := &pixelInspectionScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 4, 4))}
			wants := []uint32{0x1234, 0x5678}
			result, err := ScanFor(checked, source, tc.rects, wants, time.Millisecond, nil)
			if !errors.Is(err, context.Canceled) || result != (Result{}) || source.grabs != 0 || checked.checks != 1 {
				t.Fatalf("canceled scan captured frame: result=%+v err=%v grabs=%d checks=%d", result, err, source.grabs, checked.checks)
			}
		})
	}
}

// syntheticImage deliberately exposes only image.Image, not SubImage. Real
// screenshot wrappers and remote capture providers need not implement SubImage.
type syntheticImage struct{ img image.Image }

func (s syntheticImage) Bounds() image.Rectangle { return s.img.Bounds() }
func (s syntheticImage) ColorModel() color.Model { return s.img.ColorModel() }
func (s syntheticImage) At(x, y int) color.Color { return s.img.At(x, y) }

type syntheticScreen struct {
	source     image.Image
	zeroOrigin bool
	grabs      []image.Rectangle
}

func (s *syntheticScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.grabs = append(s.grabs, rect)
	bounds := rect
	if s.zeroOrigin {
		bounds = image.Rect(0, 0, rect.Dx(), rect.Dy())
	}
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, s.source, rect.Min, draw.Src)
	return syntheticImage{dst}, nil
}

// independentRegionHash converts precisely one requested region using
// draw.Draw and standard-library CRC32 instead of the optimized scanner.
func independentRegionHash(img image.Image, rect image.Rectangle) uint32 {
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
	return crc32.ChecksumIEEE(dst.Pix)
}

func syntheticScanFixture() (image.Image, []image.Rectangle, []uint32) {
	src := image.NewNRGBA(image.Rect(50, 70, 86, 82))
	rects := []image.Rectangle{
		image.Rect(50, 70, 62, 82),
		image.Rect(62, 70, 74, 82),
		image.Rect(74, 70, 86, 82),
	}
	for y := 70; y < 82; y++ {
		for x := 50; x < 86; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(20 + x*3), G: uint8(11 + y*2), B: uint8(x ^ y), A: uint8(150 + (x+y)%106)})
		}
	}
	wants := []uint32{0, 0, independentRegionHash(src, rects[2])}
	return src, rects, wants
}

func TestSyntheticScanCompactUsesOneCaptureWithoutSubImage(t *testing.T) {
	src, rects, wants := syntheticScanFixture()
	for _, zeroOrigin := range []bool{true, false} {
		screen := &syntheticScreen{source: src, zeroOrigin: zeroOrigin}
		result, err := ScanFor(context.Background(), screen, rects, wants, 0, nil)
		if err != nil {
			t.Fatalf("origin=%v: %v", zeroOrigin, err)
		}
		if result.Rect != rects[2] || result.Hash != wants[2] {
			t.Fatalf("origin=%v: got %+v, want region %v hash %08x", zeroOrigin, result, rects[2], wants[2])
		}
		if len(screen.grabs) != 1 || screen.grabs[0] != image.Rect(50, 70, 86, 82) {
			t.Fatalf("origin=%v: captures=%v, want one compact capture", zeroOrigin, screen.grabs)
		}
	}
}

func BenchmarkSyntheticScanCompactGeneric(b *testing.B) {
	src, rects, wants := syntheticScanFixture()
	screen := &syntheticScreen{source: src, zeroOrigin: true}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		screen.grabs = screen.grabs[:0]
		got, err := ScanFor(ctx, screen, rects, wants, 0, nil)
		if err != nil || got.Rect != rects[2] {
			b.Fatalf("result=%+v err=%v", got, err)
		}
		if len(screen.grabs) != 1 {
			b.Fatalf("performed %d backend captures instead of 1", len(screen.grabs))
		}
	}
}

// Sparse regions must not trigger a potentially huge union screenshot.
func TestSyntheticScanSparseAvoidsLargeCapture(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	rects := []image.Rectangle{image.Rect(1, 1, 13, 13), image.Rect(240, 240, 252, 252)}
	src.SetNRGBA(241, 241, color.NRGBA{R: 246, A: 255})
	wants := []uint32{0, independentRegionHash(src, rects[1])}
	screen := &syntheticScreen{source: src, zeroOrigin: true}
	result, err := ScanFor(context.Background(), screen, rects, wants, 0, nil)
	if err != nil || result.Rect != rects[1] {
		t.Fatalf("scan: %+v, %v", result, err)
	}
	if len(screen.grabs) != 2 || screen.grabs[0] != rects[0] || screen.grabs[1] != rects[1] {
		t.Fatalf("sparse scans grabbed %v instead of only the two requested regions", screen.grabs)
	}
}

func TestSyntheticScanCompactCustomHasher(t *testing.T) {
	src, rects, _ := syntheticScanFixture()
	reference := image.NewRGBA(image.Rect(0, 0, rects[2].Dx(), rects[2].Dy()))
	draw.Draw(reference, reference.Bounds(), src, rects[2].Min, draw.Src)
	h := fnv.New32a()
	_, _ = h.Write(reference.Pix)
	wants := []uint32{0, 0, h.Sum32()}
	screen := &syntheticScreen{source: src, zeroOrigin: false}
	result, err := ScanFor(context.Background(), screen, rects, wants, 0, fnv.New32a)
	if err != nil || result.Rect != rects[2] || result.Hash != wants[2] {
		t.Fatalf("custom hash result=%+v, err=%v", result, err)
	}
	if len(screen.grabs) != 1 {
		t.Fatalf("custom hash took %d captures, want 1", len(screen.grabs))
	}
}

// A dense cluster of overlapping UI watch rectangles is captured once, but
// each requested rectangle must still be checked in caller order and hashed
// against the same canonical premultiplied RGBA representation as Grab.
func syntheticOverlappingFixture() (image.Image, []image.Rectangle, []uint32) {
	source := image.NewNRGBA(image.Rect(50, 70, 94, 114))
	for y := 70; y < 114; y++ {
		for x := 50; x < 94; x++ {
			source.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 7), G: uint8(y * 3), B: uint8(x ^ y),
				A: uint8(80 + (x+y)%176),
			})
		}
	}
	rects := make([]image.Rectangle, 0, 16)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			start := image.Pt(50+4*x, 70+4*y)
			rects = append(rects, image.Rectangle{Min: start, Max: start.Add(image.Pt(32, 32))})
		}
	}
	wants := make([]uint32, len(rects))
	wants[len(wants)-1] = independentRegionHash(source, rects[len(rects)-1])
	return source, rects, wants
}

func TestSyntheticScanOverlappingCanonicalAndCaptureCount(t *testing.T) {
	src, rects, wants := syntheticOverlappingFixture()
	for _, zeroOrigin := range []bool{false, true} {
		screen := &syntheticScreen{source: src, zeroOrigin: zeroOrigin}
		got, err := ScanFor(context.Background(), screen, rects, wants, 0, nil)
		if err != nil || got.Rect != rects[len(rects)-1] || got.Hash != wants[len(wants)-1] {
			t.Fatalf("zeroOrigin=%v: got=%+v err=%v", zeroOrigin, got, err)
		}
		if len(screen.grabs) != 1 || screen.grabs[0] != src.Bounds() {
			t.Fatalf("zeroOrigin=%v: grabs=%v", zeroOrigin, screen.grabs)
		}
	}
}

func BenchmarkSyntheticScanOverlappingGeneric(b *testing.B) {
	src, rects, wants := syntheticOverlappingFixture()
	screen := &syntheticScreen{source: src, zeroOrigin: true}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		screen.grabs = screen.grabs[:0]
		got, err := ScanFor(ctx, screen, rects, wants, 0, nil)
		if err != nil || got.Rect != rects[len(rects)-1] || len(screen.grabs) != 1 {
			b.Fatalf("scan got=%+v err=%v captures=%d", got, err, len(screen.grabs))
		}
	}
}

// zeroHashScreen is a canonical-hashing backend whose region hash is zero for
// every call, which is what a region covering no pixels produced.
type zeroHashScreen struct {
	seqScreen
	regionCalls int
}

func (s *zeroHashScreen) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	s.regionCalls++
	return 0, nil
}

func (s *zeroHashScreen) CanonicalHashing() bool { return true }

// A region with no pixels cannot be watched for stability. The canonical path
// read an empty rectangle as a request for the whole screen, so the settle loop
// compared full-screen content while the caller believed it was watching a
// region, and a constant zero read as a stable region.
func TestCanonicalSettleDeclinesAnEmptyRegion(t *testing.T) {
	sc := &zeroHashScreen{seqScreen: seqScreen{frames: []image.Image{solidRGBA(color.RGBA{R: 1, A: 255})}}}
	var rect image.Rectangle
	streak := newStableStreak(0, 2)
	if probe := canonicalHashProbe(context.Background(), sc, rect, nil, &streak); probe != nil {
		t.Fatal("the canonical path accepted an empty region")
	}

	// A region that does have pixels still uses the fast path.
	real := image.Rect(0, 0, 4, 4)
	if probe := canonicalHashProbe(context.Background(), sc, real, nil, &streak); probe == nil {
		t.Fatal("the canonical path declined a real region")
	}
}

// The unit check above only proves the fast path steps aside. This proves the
// observable contract: a settle call given an empty region fails instead of
// polling the full screen and reporting that nonexistent region as stable.
func TestWaitForNoChangeRejectsAnEmptyRegion(t *testing.T) {
	sc := &zeroHashScreen{seqScreen: seqScreen{frames: []image.Image{solidRGBA(color.RGBA{R: 1, A: 255})}}}

	var rect image.Rectangle
	hash, err := WaitForNoChange(context.Background(), sc, rect, 1, time.Millisecond, nil)
	if err == nil {
		t.Fatalf("WaitForNoChange on an empty region returned hash %08x and no error", hash)
	}
	if !errors.Is(err, ErrEmptyRegion) {
		t.Fatalf("WaitForNoChange error = %v, want it to wrap ErrEmptyRegion", err)
	}
	if _, err := WaitForNoChangeFrom(context.Background(), sc, rect, 0, 1, time.Millisecond, nil); !errors.Is(err, ErrEmptyRegion) {
		t.Fatalf("WaitForNoChangeFrom error = %v, want it to wrap ErrEmptyRegion", err)
	}
	// A region with pixels is unaffected.
	if _, err := WaitForNoChange(context.Background(), sc, image.Rect(0, 0, 4, 4), 1, time.Millisecond, nil); err != nil {
		t.Fatalf("WaitForNoChange on a real region: %v", err)
	}
}

// A definite external change invalidates earlier matching hash samples.
// A subsequent matching hash must rebuild the required stable streak.
func TestStableStreakConfirmedChangeResetsEarlierSamples(t *testing.T) {
	const hash uint32 = 0x12345678
	streak := newStableStreak(hash, 3)
	if done, _ := streak.advance(hash); done {
		t.Fatal("two matching samples declared three-sample stability")
	}
	streak.changed()
	if streak.count != 0 {
		t.Fatalf("confirmed change retained %d previous samples", streak.count)
	}
	for i := 1; i < 3; i++ {
		if done, got := streak.advance(hash); done || got != hash {
			t.Fatalf("post-change sample %d: done=%v hash=%x", i, done, got)
		}
	}
	if done, got := streak.advance(hash); !done || got != hash {
		t.Fatalf("third post-change hash did not establish stability: done=%v hash=%x", done, got)
	}
}

type failingCanonicalCapture struct {
	failure error
	hashes  int
	grabs   int
}

func (f *failingCanonicalCapture) CanonicalHashing() bool { return true }
func (f *failingCanonicalCapture) Grab(context.Context, image.Rectangle) (image.Image, error) {
	f.grabs++
	return nil, nil
}
func (f *failingCanonicalCapture) GrabFullHash(context.Context) (uint32, error) { return 0, f.failure }
func (f *failingCanonicalCapture) GrabRegionHash(context.Context, image.Rectangle) (uint32, error) {
	f.hashes++
	return 0, f.failure
}

// A failed canonical fingerprint is not a zero-valued observation that a
// settle loop may count as stable. The failure must propagate unchanged.
func TestCanonicalHashProbeDoesNotCountFailedCaptureAsStable(t *testing.T) {
	cause := errors.New("synthetic private capture authorization revoked")
	sc := &failingCanonicalCapture{failure: cause}
	streak := newStableStreak(2, 73)
	probe := canonicalHashProbe(context.Background(), sc, image.Rect(0, 0, 3, 3), nil, &streak)
	if probe == nil {
		t.Fatal("canonical capture probe not constructed")
	}
	done, hash, err := probe(0)
	if done || hash != 0 || !errors.Is(err, cause) || sc.hashes != 1 || sc.grabs != 0 {
		t.Fatalf("failed canonical hash accepted: done=%v hash=%d err=%v calls=%d/%d", done, hash, err, sc.hashes, sc.grabs)
	}
}

// Image search must reject missing references and unsuccessful generic-image
// comparisons without inventing a match or reading invalid backing buffers.
func TestExactImageSearchRejectsNilReferenceAndGenericNonmatch(t *testing.T) {
	source := image.NewGray(image.Rect(5, 7, 8, 10))
	reference := image.NewGray(image.Rect(0, 0, 1, 1))
	reference.SetGray(0, 0, color.Gray{Y: 255})
	if got, err := LocateExactInImage(source, source.Bounds(), nil); err == nil || !got.Empty() {
		t.Fatalf("missing reference accepted: %v %v", got, err)
	}
	got, err := LocateExactInImage(source, source.Bounds(), reference)
	if !errors.Is(err, ErrNotFound) || !got.Empty() {
		t.Fatalf("nonmatching gray reference accepted: %v %v", got, err)
	}
}

func TestPixelSearchRejectsMissingAndInvalidPackedFrames(t *testing.T) {
	target := color.RGBA{R: 17, A: 255}
	if point, found := PixelFound(nil, image.Rect(0, 0, 1, 1), target, 0); found || point != (image.Point{}) {
		t.Fatalf("missing image reported match %v %v", point, found)
	}
	bounds := image.Rect(0, 0, 2, 2)
	if point, found := scanPackedPixels(make([]byte, 16), bounds, bounds, 3, image.Point{}, target, 0, false); found || point != (image.Point{}) {
		t.Fatalf("malformed packed stride reported match %v %v", point, found)
	}
}

type zeroOriginScreenshotter struct {
	frame image.Image
}

func (z *zeroOriginScreenshotter) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	target := rect
	if target.Empty() {
		target = z.frame.Bounds()
	}
	target = target.Intersect(z.frame.Bounds())
	if target.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 0, 0)), nil
	}
	out := image.NewRGBA(image.Rect(0, 0, target.Dx(), target.Dy()))
	draw.Draw(out, out.Bounds(), z.frame, target.Min, draw.Src)
	return out, nil
}

func (z *zeroOriginScreenshotter) GrabFullHash(ctx context.Context) (uint32, error) {
	return PixelHash(z.frame, nil), nil
}

func (z *zeroOriginScreenshotter) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	img, err := z.Grab(ctx, rect)
	if err != nil {
		return 0, err
	}
	return PixelHash(img, nil), nil
}

type fullFrameScreenshotter struct {
	frame image.Image
}

func (s fullFrameScreenshotter) Grab(context.Context, image.Rectangle) (image.Image, error) {
	return s.frame, nil
}

func TestLocateExactZeroOriginCapture(t *testing.T) {
	full := image.NewRGBA(image.Rect(10, 20, 30, 40))
	needle := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for y := range 3 {
		for x := range 3 {
			c := color.RGBA{R: 10, G: uint8(20 + x), B: uint8(30 + y), A: 255}
			full.SetRGBA(14+x, 23+y, c)
			needle.SetRGBA(x, y, c)
		}
	}

	sc := &zeroOriginScreenshotter{frame: full}
	found, err := LocateExact(context.Background(), sc, image.Rect(10, 20, 30, 40), needle)
	if err != nil {
		t.Fatal(err)
	}
	want := image.Rect(14, 23, 17, 26)
	if found != want {
		t.Fatalf("found = %v, want %v", found, want)
	}
}

func TestScanForZeroOriginCapture(t *testing.T) {
	full := image.NewRGBA(image.Rect(10, 20, 30, 40))
	region1 := image.Rect(12, 22, 14, 24)
	region2 := image.Rect(16, 24, 18, 26)
	for y := region1.Min.Y; y < region1.Max.Y; y++ {
		for x := region1.Min.X; x < region1.Max.X; x++ {
			full.SetRGBA(x, y, color.RGBA{R: 1, G: 2, B: 3, A: 255})
		}
	}
	for y := region2.Min.Y; y < region2.Max.Y; y++ {
		for x := region2.Min.X; x < region2.Max.X; x++ {
			full.SetRGBA(x, y, color.RGBA{R: 9, G: 8, B: 7, A: 255})
		}
	}

	ref1 := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			ref1.SetRGBA(x, y, color.RGBA{R: 1, G: 2, B: 3, A: 255})
		}
	}
	want1 := PixelHash(ref1, nil)
	ref2 := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := range 2 {
		for x := range 2 {
			ref2.SetRGBA(x, y, color.RGBA{R: 9, G: 8, B: 7, A: 255})
		}
	}
	want2 := PixelHash(ref2, nil)

	sc := &zeroOriginScreenshotter{frame: full}
	res, err := ScanFor(context.Background(), sc, []image.Rectangle{region1, region2}, []uint32{want1, want2}, 1*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Hash != want1 {
		t.Fatalf("res.Hash = %08x, want %08x", res.Hash, want1)
	}
	if res.Rect != region1 {
		t.Fatalf("res.Rect = %v, want %v", res.Rect, region1)
	}
}

func TestFindColorZeroOriginCapture(t *testing.T) {
	full := image.NewRGBA(image.Rect(10, 20, 30, 40))
	target := color.RGBA{R: 23, G: 45, B: 67, A: 255}
	full.SetRGBA(13, 24, target)

	sc := &zeroOriginScreenshotter{frame: full}
	p, err := FindColor(context.Background(), sc, image.Rect(10, 20, 30, 40), target, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := image.Pt(13, 24)
	if p != want {
		t.Fatalf("p = %v, want %v", p, want)
	}
}

func TestFindColorFullScreenCapturePreservesImageOrigin(t *testing.T) {
	bounds := image.Rect(10, 20, 30, 40)
	want := image.Pt(13, 24)
	gray := color.Gray{Y: 128}

	tests := []struct {
		name   string
		frame  image.Image
		target color.RGBA
	}{
		{
			name: "rgba",
			frame: func() image.Image {
				img := image.NewRGBA(bounds)
				img.SetRGBA(want.X, want.Y, color.RGBA{R: 23, G: 45, B: 67, A: 255})
				return img
			}(),
			target: color.RGBA{R: 23, G: 45, B: 67, A: 255},
		},
		{
			name: "generic image",
			frame: func() image.Image {
				img := image.NewGray(bounds)
				img.SetGray(want.X, want.Y, gray)
				return img
			}(),
			target: color.RGBA{R: gray.Y, G: gray.Y, B: gray.Y, A: 255},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindColor(context.Background(), fullFrameScreenshotter{frame: tt.frame}, image.Rectangle{}, tt.target, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("FindColor point = %v, want %v", got, want)
			}
		})
	}
}

func TestFindColorFullFrameImageHonorsRequestedRegion(t *testing.T) {
	frame := image.NewRGBA(image.Rect(10, 20, 30, 40))
	target := color.RGBA{R: 23, G: 45, B: 67, A: 255}
	frame.SetRGBA(10, 20, target)
	frame.SetRGBA(15, 26, target)

	got, err := FindColor(
		context.Background(),
		fullFrameScreenshotter{frame: frame},
		image.Rect(13, 24, 18, 29),
		target,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := image.Pt(15, 26); got != want {
		t.Fatalf("FindColor point = %v, want %v", got, want)
	}
}
