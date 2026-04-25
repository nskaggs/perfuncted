package find

import (
	"context"
	"hash/crc32"
	"image"
	"image/color"
	"testing"
	"time"
)

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
	customHasher := crc32.NewIEEE
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
	pred := func(img image.Image) bool {
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

	pred := func(img image.Image) bool {
		return false // never true
	}

	_, err := WaitForFn(ctx, sc, image.Rect(0, 0, 10, 10), pred, 10*time.Millisecond)
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
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
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
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
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

// TestScanFor tests ScanFor across multiple regions.
func TestScanFor(t *testing.T) {
	// Create an image with patterns at different locations
	img := image.NewRGBA(image.Rect(0, 0, 30, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 30; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 100, G: 100, B: 100, A: 255})
		}
	}

	// Place a unique pattern at (5,5) and (15,15)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
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

// TestWaitWithTolerance tests WaitWithTolerance.
func TestWaitWithTolerance(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// Fill with mostly gray but one pixel is slightly different
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 100, G: 100, B: 100, A: 255})
		}
	}
	// Place a specific pattern at (4,4)
	ref := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			ref.SetRGBA(x, y, color.RGBA{R: 150, G: 150, B: 150, A: 255})
			img.SetRGBA(4+x, 4+y, color.RGBA{R: 150, G: 150, B: 150, A: 255})
		}
	}

	sc := &fakeScreen{img: img}
	hash := PixelHash(ref, nil)

	resultHash, resultRect, err := WaitWithTolerance(context.Background(), sc, image.Rect(4, 4, 6, 6), hash, 2, 1*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("WaitWithTolerance returned error: %v", err)
	}
	if resultHash != hash {
		t.Fatalf("WaitWithTolerance returned hash %08x, want %08x", resultHash, hash)
	}
	if resultRect.Min.X != 4 || resultRect.Min.Y != 4 {
		t.Fatalf("WaitWithTolerance returned rect %v, want around (4,4)", resultRect)
	}
}

// TestFindColor tests FindColor function.
func TestFindColor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// Fill with gray
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
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
}

// TestWaitForLocate tests WaitForLocate.
func TestWaitForLocate(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	needle := image.NewRGBA(image.Rect(0, 0, 3, 3))

	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
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
