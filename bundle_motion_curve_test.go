package perfuncted

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestMotionCurveEnvelopeHorizontalVerticalDiagonal(t *testing.T) {
	cases := []struct {
		name   string
		a, b   motionPoint
		curved func(motionPoint) bool
	}{
		{"horizontal", motionPoint{0, 0}, motionPoint{100, 0}, func(p motionPoint) bool { return p.y > 0 }},
		{"vertical", motionPoint{0, 0}, motionPoint{0, 100}, func(p motionPoint) bool { return p.x < 0 }},
		{"diagonal", motionPoint{0, 0}, motionPoint{100, 100}, func(p motionPoint) bool { return p.x < 0 || p.y > 100 || p.x < p.y }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := MotionOptions{Duration: 160 * time.Millisecond, Curve: .5}
			points := motionSamples(tc.a, tc.b, opts)
			if points[0] != tc.a || points[len(points)-1] != tc.b {
				t.Fatalf("inexact endpoints: %v", points)
			}
			sawCurve := false
			for _, p := range points {
				if tc.curved(p) {
					sawCurve = true
				}
			}
			if !sawCurve {
				t.Fatalf("curve collapsed: %v", points)
			}
			for _, seed := range []uint64{1, 42} {
				varied := opts
				varied.Variation = 16
				varied.Seed = seed
				withVariation := motionSamples(tc.a, tc.b, varied)
				if !reflect.DeepEqual(withVariation, motionSamples(tc.a, tc.b, varied)) {
					t.Fatal("variation not deterministic")
				}
				dx, dy := float64(tc.b.x)-float64(tc.a.x), float64(tc.b.y)-float64(tc.a.y)
				minX, maxX := math.Min(float64(tc.a.x), float64(tc.b.x)), math.Max(float64(tc.a.x), float64(tc.b.x))
				minY, maxY := math.Min(float64(tc.a.y), float64(tc.b.y)), math.Max(float64(tc.a.y), float64(tc.b.y))
				marginX, marginY := .5*math.Abs(dy)+16, .5*math.Abs(dx)+16
				for _, p := range withVariation {
					if float64(p.x) < minX-marginX-1 || float64(p.x) > maxX+marginX+1 || float64(p.y) < minY-marginY-1 || float64(p.y) > maxY+marginY+1 {
						t.Fatalf("outside bounded envelope: %+v", p)
					}
				}
				if withVariation[0] != tc.a || withVariation[len(withVariation)-1] != tc.b {
					t.Fatal("variation displaced endpoints")
				}
			}
		})
	}
}
func TestMotionSaturatedCoordinateEnvelope(t *testing.T) {
	max := int(^uint(0) >> 1)
	min := -max - 1
	for _, tc := range [][2]motionPoint{{{max, max}, {min, min}}, {{max, max}, {max - 1, max}}, {{min, min}, {min + 1, min}}} {
		points := motionSamples(tc[0], tc[1], MotionOptions{Duration: 10 * time.Second, Curve: .5, Variation: 16})
		if points[0] != tc[0] || points[len(points)-1] != tc[1] {
			t.Fatal("extreme endpoints changed")
		}
		for _, p := range points {
			if p.x < min || p.x > max || p.y < min || p.y > max {
				t.Fatalf("overflow %v", p)
			}
		}
	}
}
