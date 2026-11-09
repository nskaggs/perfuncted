package screen

import (
	"context"
	"image"
	"strings"
	"testing"
)

// A partially constructed screencopy backend is not an authorized compositor
// connection. Public capture methods must return errors, not panic or spawn
// a janitor against uninitialized state.
func TestWlrUninitializedBackendFailsClosedWithoutPanic(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*WlrScreencopyBackend) error
	}{
		{name: "full screenshot", run: func(b *WlrScreencopyBackend) error {
			_, err := b.Grab(context.Background(), image.Rectangle{})
			return err
		}},
		{name: "full hash", run: func(b *WlrScreencopyBackend) error { _, err := b.GrabFullHash(context.Background()); return err }},
		{name: "region hash", run: func(b *WlrScreencopyBackend) error {
			_, err := b.GrabRegionHash(context.Background(), image.Rect(1, 2, 3, 4))
			return err
		}},
		{name: "resolution", run: func(b *WlrScreencopyBackend) error {
			_, _, err := b.ResolutionWithContext(context.Background())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &WlrScreencopyBackend{}
			err := tc.run(b)
			if err == nil || !strings.Contains(err.Error(), "uninitialized") {
				t.Fatalf("uninitialized capture error = %v", err)
			}
			if closeErr := b.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		})
	}
}
