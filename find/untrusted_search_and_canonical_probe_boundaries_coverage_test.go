package find

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
)

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
