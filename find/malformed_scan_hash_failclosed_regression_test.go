package find

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

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
