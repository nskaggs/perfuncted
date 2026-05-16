package find

import (
	"context"
	"image"
	"testing"
	"time"
)

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
