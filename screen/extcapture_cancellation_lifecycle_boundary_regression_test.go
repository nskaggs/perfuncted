package screen

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/find"
)

// A canceled capture may not consult an uninitialized compositor session,
// publish an image, or return a misleading backend-configuration error.
func TestExtCaptureCancelledRequestsFailBeforeBackendOrPixelAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range []struct {
		name string
		run  func(*ExtCaptureBackend) error
	}{
		{"full screen", func(b *ExtCaptureBackend) error {
			hash, err := b.GrabFullHash(ctx)
			if hash != 0 {
				t.Fatalf("canceled full frame disclosed hash %x", hash)
			}
			return err
		}},
		{"region", func(b *ExtCaptureBackend) error {
			hash, err := b.GrabRegionHash(ctx, image.Rect(0, 0, 32, 32))
			if hash != 0 {
				t.Fatalf("canceled region disclosed hash %x", hash)
			}
			return err
		}},
		{"image", func(b *ExtCaptureBackend) error {
			img, err := b.Grab(ctx, image.Rect(0, 0, 32, 32))
			if img != nil {
				t.Fatalf("canceled capture disclosed image %v", img)
			}
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(&ExtCaptureBackend{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled capture returned %v", err)
			}
		})
	}
}

// A shutdown backend must not reopen a screenshot session or accidentally
// substitute an invalid-session error for its authoritative closed state.
func TestExtCaptureClosedBackendRejectsSubsequentReads(t *testing.T) {
	backend := &ExtCaptureBackend{}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Grab(context.Background(), image.Rect(0, 0, 1, 1)); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed capture returned %v", err)
	}
	if _, err := backend.GrabFullHash(context.Background()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed hash returned %v", err)
	}
	if _, err := backend.GrabRegionHash(context.Background(), image.Rect(0, 0, 1, 1)); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed region hash returned %v", err)
	}
}

// An invalid output scale is not authority to reject a legitimate request or
// remap it to a different pixel region; the documented fallback is scale one.
func TestExtCaptureInvalidHashScaleKeepsLogicalRegion(t *testing.T) {
	request := image.Rect(4, 5, 7, 9)
	exact, err := croppedHashRegion(request, 1, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, scale := range []int{0, -1, -999} {
		got, err := croppedHashRegion(request, scale, 20, 20)
		if err != nil || got != exact {
			t.Fatalf("scale=%d redirected region %v: %v", scale, got, err)
		}
	}
	if _, err := croppedHashRegion(image.Rect(30, 30, 33, 33), 0, 20, 20); !errors.Is(err, find.ErrEmptyRegion) {
		t.Fatalf("out-of-frame region accepted: %v", err)
	}
}
