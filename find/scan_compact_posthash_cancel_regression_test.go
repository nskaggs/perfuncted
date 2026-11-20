package find

import (
	"context"
	"errors"
	"hash"
	"hash/crc32"
	"image"
	"testing"
)

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
