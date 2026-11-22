package find

import (
	"context"
	"image"
	"math"
)

// templateRank stores a position as a compact linear offset into the candidate
// grid. Equal floating-point confidence scores rank by the original y/x scan
// order. The capture size limit ensures positions fit in uint32.
type templateRank struct {
	confidence float64
	index      uint32
}

// betterTemplateRank implements descending confidence and ascending y/x.
func betterTemplateRank(a, b templateRank) bool {
	if a.confidence != b.confidence {
		return a.confidence > b.confidence
	}
	return a.index < b.index
}

// templateRankHeap retains the best N placements with its WORST element at
// the root. Consequently a new placement can be rejected in O(1), or admitted
// in O(log N), without retaining every qualifying pixel.
type templateRankHeap []templateRank

func (h templateRankHeap) pushTop(candidate templateRank, limit int) templateRankHeap {
	if len(h) == limit {
		if !betterTemplateRank(candidate, h[0]) {
			return h
		}
		h[0] = candidate
		h.down(0, false)
		return h
	}
	h = append(h, candidate)
	for i := len(h) - 1; i > 0; {
		parent := (i - 1) / 2
		if !betterTemplateRank(h[parent], h[i]) {
			break
		}
		h[parent], h[i] = h[i], h[parent]
		i = parent
	}
	return h
}

// down works for the worst-first retention heap and the best-first extraction
// heap, selected by bestFirst. It never allocates.
func (h templateRankHeap) down(i int, bestFirst bool) {
	for {
		left := i*2 + 1
		if left >= len(h) {
			return
		}
		child := left
		if right := left + 1; right < len(h) {
			if (bestFirst && betterTemplateRank(h[right], h[left])) ||
				(!bestFirst && betterTemplateRank(h[left], h[right])) {
				child = right
			}
		}
		if (bestFirst && !betterTemplateRank(h[child], h[i])) ||
			(!bestFirst && !betterTemplateRank(h[i], h[child])) {
			return
		}
		h[i], h[child] = h[child], h[i]
		i = child
	}
}

func (h templateRankHeap) buildBestFirst(ctx context.Context) error {
	for i := len(h)/2 - 1; i >= 0; i-- {
		if i%1024 == 0 {
			if err := contextErr(ctx); err != nil {
				return err
			}
		}
		h.down(i, true)
	}
	return contextErr(ctx)
}

func (h templateRankHeap) popBest() (templateRank, templateRankHeap) {
	best := h[0]
	last := len(h) - 1
	h[0] = h[last]
	h = h[:last]
	if len(h) > 0 {
		h.down(0, true)
	}
	return best, h
}

// templateRetentionLimit is a provably sufficient top-rank prefix for greedy
// suppression. One selected rectangle overlaps at most D pixel placements.
// Before the Kth surviving match, at most (K-1)*(D-1) higher-ranked
// placements can have been removed by earlier survivors. Therefore the first
// K*D candidates contain every possible survivor in the first K results.
//
// Integer screenshot-to-desktop scaling can make adjacent pixel placements
// share a logical rectangle. The conservative bound expands each radius by
// the integer scale. It may overretain candidates; it cannot underretain them.
// Memory is O(min(candidate placements, K*D)), not always O(K). The 16-Mpixel
// capture cap bounds even the worst case, but a very large near-uniform
// template can require substantial candidate storage.
func templateRetentionLimit(g templateGeometry, maxResults int) int {
	columns := g.sourceW - g.referenceW + 1
	rows := g.sourceH - g.referenceH + 1
	scale := g.sourceW / g.screenW
	radiusX := 2*(g.referenceW+scale-1) - 1
	radiusY := 2*(g.referenceH+scale-1) - 1
	overlapWidth := min(columns, radiusX)
	overlapHeight := min(rows, radiusY)
	total := int64(columns) * int64(rows)
	bound := int64(maxResults) * int64(overlapWidth) * int64(overlapHeight)
	if bound > total {
		return int(total)
	}
	return int(bound)
}

func collectTemplateMatches(ctx context.Context, source *image.NRGBA, samples []templateSample, weightSum uint64, g templateGeometry, minConfidence float64, maxResults int) ([]TemplateMatch, error) {
	columns := g.sourceW - g.referenceW + 1
	rows := g.sourceH - g.referenceH + 1
	limit := templateRetentionLimit(g, maxResults)
	heap := make(templateRankHeap, 0, min(limit, 4096))
	maxDiff := float64(weightSum) * 3 * 255
	const cancelStride = 128
	for y := 0; y < rows; y++ {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		for x := 0; x < columns; x++ {
			if x%cancelStride == 0 {
				if err := contextErr(ctx); err != nil {
					return nil, err
				}
			}
			floor := minConfidence
			if len(heap) == limit && heap[0].confidence > floor {
				floor = heap[0].confidence
			}
			// Reuse the same alpha-weighted SAD and upward-rounded prune budget as
			// the best-only path. The final floating-point confidence check preserves
			// score ties and exact threshold boundaries.
			budget := math.Ceil((1 - floor) * maxDiff)
			diff, rejected, err := templateCandidateDiff(ctx, source.Pix, y*source.Stride+x*4, samples, budget)
			if err != nil {
				return nil, err
			}
			if rejected {
				continue
			}
			confidence := 1 - float64(diff)/maxDiff
			if confidence < minConfidence {
				continue
			}
			heap = heap.pushTop(templateRank{confidence: confidence, index: uint32(y*columns + x)}, limit)
		}
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if err := heap.buildBestFirst(ctx); err != nil {
		return nil, err
	}
	return extractTemplateMatches(ctx, heap, g, columns, maxResults)
}

// extractTemplateMatches consumes retained placements in exact priority order,
// applying the same greedy overlap policy as repeated best-match scans.
func extractTemplateMatches(ctx context.Context, heap templateRankHeap, g templateGeometry, columns, maxResults int) ([]TemplateMatch, error) {
	matches := make([]TemplateMatch, 0, maxResults)
	for len(heap) > 0 && len(matches) < maxResults {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		var candidate templateRank
		candidate, heap = heap.popBest()
		x, y := int(candidate.index)%columns, int(candidate.index)/columns
		rect := templateScreenRect(x, y, g)
		overlaps := false
		for _, earlier := range matches {
			if rect.Overlaps(earlier.Rect) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			matches = append(matches, TemplateMatch{Rect: rect, Confidence: candidate.confidence})
		}
	}
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	return matches, nil
}
