package find

import (
	"context"
	"image"
	"image/color"
	"testing"
	"time"
)

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
