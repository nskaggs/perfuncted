//go:build linux

package screen

import (
	"context"
	"image"
	"testing"
)

// A compositor transport that produces no frame must not turn into a valid
// zero hash or a panic in the public resolution API.
func TestKWinShotRejectsMissingFrameAcrossOperations(t *testing.T) {
	b := &KWinShotBackend{transport: &fakeKWinTransport{}}
	ctx := context.Background()
	checks := []struct {
		name   string
		invoke func() error
	}{
		{"active capture", func() error { _, err := b.Grab(ctx, image.Rectangle{}); return err }},
		{"area capture", func() error { _, err := b.Grab(ctx, image.Rect(0, 0, 3, 2)); return err }},
		{"full hash", func() error { _, err := b.GrabFullHash(ctx); return err }},
		{"region hash", func() error { _, err := b.GrabRegionHash(ctx, image.Rect(0, 0, 3, 2)); return err }},
		{"resolution", func() error { _, _, err := b.Resolution(); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.invoke(); err == nil {
				t.Fatal("nil capture was accepted as a successful operation")
			}
		})
	}
}
