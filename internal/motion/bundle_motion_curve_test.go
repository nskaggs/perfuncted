package motion

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestMotionCurveEnvelopeHorizontalVerticalDiagonal(t *testing.T) {
	cases := []struct {
		name   string
		a, b   Point
		curved func(Point) bool
	}{
		{"horizontal", Point{0, 0}, Point{100, 0}, func(p Point) bool { return p.Y > 0 }},
		{"vertical", Point{0, 0}, Point{0, 100}, func(p Point) bool { return p.X < 0 }},
		{"diagonal", Point{0, 0}, Point{100, 100}, func(p Point) bool { return p.X < 0 || p.Y > 100 || p.X < p.Y }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{Duration: 160 * time.Millisecond, Curve: .5}
			points := Samples(tc.a, tc.b, opts)
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
				withVariation := Samples(tc.a, tc.b, varied)
				if !reflect.DeepEqual(withVariation, Samples(tc.a, tc.b, varied)) {
					t.Fatal("variation not deterministic")
				}
				dx, dy := float64(tc.b.X)-float64(tc.a.X), float64(tc.b.Y)-float64(tc.a.Y)
				minX, maxX := math.Min(float64(tc.a.X), float64(tc.b.X)), math.Max(float64(tc.a.X), float64(tc.b.X))
				minY, maxY := math.Min(float64(tc.a.Y), float64(tc.b.Y)), math.Max(float64(tc.a.Y), float64(tc.b.Y))
				marginX, marginY := .5*math.Abs(dy)+16, .5*math.Abs(dx)+16
				for _, p := range withVariation {
					if float64(p.X) < minX-marginX-1 || float64(p.X) > maxX+marginX+1 || float64(p.Y) < minY-marginY-1 || float64(p.Y) > maxY+marginY+1 {
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
	for _, tc := range [][2]Point{{{max, max}, {min, min}}, {{max, max}, {max - 1, max}}, {{min, min}, {min + 1, min}}} {
		points := Samples(tc[0], tc[1], Options{Duration: 10 * time.Second, Curve: .5, Variation: 16})
		if points[0] != tc[0] || points[len(points)-1] != tc[1] {
			t.Fatal("extreme endpoints changed")
		}
		for _, p := range points {
			if p.X < min || p.X > max || p.Y < min || p.Y > max {
				t.Fatalf("overflow %v", p)
			}
		}
	}
}
