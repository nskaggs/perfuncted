package find

import (
	"context"
	"image"
	"image/color"
	"testing"
)

// zeroHashScreen is a canonical-hashing backend whose region hash is zero for
// every call, which is what a region covering no pixels produced.
type zeroHashScreen struct {
	seqScreen
	regionCalls int
}

func (s *zeroHashScreen) GrabRegionHash(ctx context.Context, rect image.Rectangle) (uint32, error) {
	s.regionCalls++
	return 0, nil
}

func (s *zeroHashScreen) CanonicalHashing() bool { return true }

// A region with no pixels cannot be watched for stability. The canonical path
// read an empty rectangle as a request for the whole screen, so the settle loop
// compared full-screen content while the caller believed it was watching a
// region, and a constant zero read as a stable region.
func TestCanonicalSettleDeclinesAnEmptyRegion(t *testing.T) {
	sc := &zeroHashScreen{seqScreen: seqScreen{frames: []image.Image{solidRGBA(color.RGBA{R: 1, A: 255})}}}
	var rect image.Rectangle
	streak := newStableStreak(0, 2)
	if probe := canonicalHashProbe(context.Background(), sc, rect, nil, &streak); probe != nil {
		t.Fatal("the canonical path accepted an empty region")
	}

	// A region that does have pixels still uses the fast path.
	real := image.Rect(0, 0, 4, 4)
	if probe := canonicalHashProbe(context.Background(), sc, real, nil, &streak); probe == nil {
		t.Fatal("the canonical path declined a real region")
	}
}
