package screen

import (
	"context"
	"image"
	"strings"
	"testing"
)

func TestExtCaptureIncompleteSessionFailsClosedWithoutPanic(t *testing.T) {
	for _, b := range []*ExtCaptureBackend{nil, {}, {session: nil}} {
		for _, op := range []struct {
			name string
			run  func() error
		}{
			{"grab", func() error {
				img, err := b.Grab(context.Background(), image.Rect(1, 2, 3, 4))
				if img != nil {
					t.Fatalf("invalid backend returned image %v", img)
				}
				return err
			}},
			{"full hash", func() error { _, err := b.GrabFullHash(context.Background()); return err }},
			{"region hash", func() error { _, err := b.GrabRegionHash(context.Background(), image.Rect(1, 2, 3, 4)); return err }},
		} {
			t.Run(op.name, func(t *testing.T) {
				err := op.run()
				if err == nil || (!strings.Contains(err.Error(), "not initialised") && !strings.Contains(err.Error(), "nil")) {
					t.Fatalf("incomplete backend accepted operation: %v", err)
				}
			})
		}
	}
}
