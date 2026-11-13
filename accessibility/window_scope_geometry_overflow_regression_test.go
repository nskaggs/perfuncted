package accessibility

import (
	"math"
	"testing"
)

// Window identity cannot depend on signed-integer wraparound in coordinates
// or area. AT-SPI rectangle values are untrusted provider data.
func TestWindowCandidateGeometryRejectsOverflowDrivenFalseNegatives(t *testing.T) {
	cases := []struct {
		name         string
		node, target Rect
	}{
		{"far positive endpoint", Rect{X: 40, Y: 20, Width: 20, Height: 10}, Rect{X: 30, Y: 20, Width: math.MaxInt, Height: 10}},
		{"far negative endpoint", Rect{X: -40, Y: 20, Width: 20, Height: 10}, Rect{X: math.MinInt + 4, Y: 20, Width: math.MaxInt, Height: 10}},
		{"area multiplication", Rect{X: 0, Y: 0, Width: 1 << 40, Height: 1 << 30}, Rect{X: 0, Y: 0, Width: 1 << 40, Height: 1 << 30}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rectOverlap(tc.node, tc.target); got <= 0 {
				t.Errorf("actual positive overlap rejected, got=%d", got)
			}
			target := WindowTarget{ID: "managed", Bounds: tc.target}
			node := Node{Name: "Editor", Role: "frame", HasBounds: true, Bounds: tc.node}
			if score := windowCandidateScore(node, target); score == 0 {
				t.Error("valid geometric window correlation was rejected")
			}
		})
	}
}

func TestWindowCandidateGeometryOverflowNeverInventsIntersection(t *testing.T) {
	cases := []struct {
		name string
		a, b Rect
		want int
	}{
		{"ordinary overlap", Rect{X: 2, Y: 3, Width: 8, Height: 10}, Rect{X: 5, Y: 9, Width: 10, Height: 6}, 20},
		{"adjacent rectangles", Rect{X: 0, Y: 0, Width: 10, Height: 10}, Rect{X: 10, Y: 0, Width: 10, Height: 10}, 0},
		{"separated extremes", Rect{X: math.MinInt, Y: 0, Width: 2, Height: 10}, Rect{X: math.MaxInt - 2, Y: 0, Width: 2, Height: 10}, 0},
		{"invalid width", Rect{X: 0, Y: 0, Width: -1, Height: 10}, Rect{X: 0, Y: 0, Width: 10, Height: 10}, 0},
		{"invalid height", Rect{X: 0, Y: 0, Width: 10, Height: 0}, Rect{X: 0, Y: 0, Width: 10, Height: 10}, 0},
		{"saturating area", Rect{X: 0, Y: 0, Width: 1 << 40, Height: 1 << 30}, Rect{X: 0, Y: 0, Width: 1 << 40, Height: 1 << 30}, math.MaxInt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rectOverlap(tc.a, tc.b); got != tc.want {
				t.Fatalf("overlap=%d, want=%d", got, tc.want)
			}
			if got := rectOverlap(tc.b, tc.a); got != tc.want {
				t.Fatalf("reversed overlap=%d, want=%d", got, tc.want)
			}
		})
	}
}
