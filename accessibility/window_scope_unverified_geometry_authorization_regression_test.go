package accessibility

import (
	"context"
	"errors"
	"testing"
)

// Geometry alone is authoritative correlation evidence only if the AT-SPI
// window's own bounds have actually been verified. A matched application name
// and a single frame cannot substitute for absent component geometry.
func TestPrivateATSPIGeometryOnlyCorrelationCannotGuessUnverifiedFrame(t *testing.T) {
	backend, _, target := newSyntheticATSPIBackend(t, false)
	target.PID = 0
	target.PIDHint = 0
	target.Title = ""
	target.Active = false
	target.Focused = false
	target.AppID = "Editor"
	target.Bounds = Rect{X: 20000, Y: 20000, Width: 400, Height: 300}
	for _, fresh := range []bool{false, true} {
		t.Run(map[bool]string{false: "cached", true: "fresh"}[fresh], func(t *testing.T) {
			var scope WindowScope
			var err error
			if fresh {
				scope, err = backend.ResolveWindowFresh(context.Background(), target)
			} else {
				scope, err = backend.ResolveWindow(context.Background(), target)
			}
			if !errors.Is(err, ErrNotFound) || scope.Root.BusName != "" {
				t.Fatalf("geometry-only correlation guessed unrelated frame: scope=%+v error=%v", scope, err)
			}
		})
	}
}

// Positive evidence still correlates: the fail-closed checks apply to missing
// evidence, not legitimately observed geometry or focus state.
func TestWindowEvidenceChecksPreserveVerifiedGeometryAndFocus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		node   Node
		target WindowTarget
	}{
		{name: "verified overlap", node: Node{Role: "frame", HasBounds: true, Bounds: Rect{X: 50, Y: 30, Width: 200, Height: 150}}, target: WindowTarget{ID: "private", AppID: "Editor", Bounds: Rect{X: 100, Y: 70, Width: 100, Height: 100}}},
		{name: "verified focus", node: Node{Role: "frame", Focused: true}, target: WindowTarget{ID: "private", AppID: "Editor", Active: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if score := windowCandidateScore(tc.node, tc.target); score <= 1 {
				t.Fatalf("verified window evidence failed to contribute to correlation: %d", score)
			}
		})
	}
}
