package perfuncted

import (
	"math"
	"testing"
	"time"
)

func TestMotionSamplerCapacityReverseAndExtremeCoordinates(t *testing.T) {
	opts := MotionOptions{Duration: 10 * time.Second, Curve: -.5, Variation: 16, Seed: 7}
	max := int(^uint(0) >> 1)
	min := -max - 1
	for _, tc := range [][2]motionPoint{
		{{100, 100}, {-100, -100}}, {{max, max}, {min, min}}, {{max - 1, max - 1}, {max, max}}, {{min, min}, {min + 1, min + 1}},
	} {
		points := motionSamples(tc[0], tc[1], opts)
		if len(points) != motionMaxSamples+1 {
			t.Fatalf("unbounded samples %d", len(points))
		}
		if points[0] != tc[0] || points[len(points)-1] != tc[1] {
			t.Fatalf("endpoints: %v", points)
		}
		loX, hiX := tc[0].x, tc[1].x
		if loX > hiX {
			loX, hiX = hiX, loX
		}
		loY, hiY := tc[0].y, tc[1].y
		if loY > hiY {
			loY, hiY = hiY, loY
		}
		dx, dy := float64(tc[1].x)-float64(tc[0].x), float64(tc[1].y)-float64(tc[0].y)
		mx, my := .5*math.Abs(dy)+float64(opts.Variation), .5*math.Abs(dx)+float64(opts.Variation)
		for _, p := range points {
			if float64(p.x) < float64(loX)-mx-1 || float64(p.x) > float64(hiX)+mx+1 ||
				float64(p.y) < float64(loY)-my-1 || float64(p.y) > float64(hiY)+my+1 {
				t.Fatalf("outside motion envelope: %v", p)
			}
		}
	}
	short := motionSamples(motionPoint{1, 2}, motionPoint{3, 4}, MotionOptions{Duration: time.Millisecond})
	if len(short) != 3 {
		t.Fatalf("minimum samples %v", short)
	}
	err := ValidateMotionOptions(MotionOptions{Duration: -1})
	if err == nil {
		t.Fatal("accepted invalid duration")
	}
	if math.IsNaN(opts.Curve) {
		t.Fatal("unexpected NaN")
	}
}
