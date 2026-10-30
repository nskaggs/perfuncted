package input

import (
	"math"
	"math/rand"
	"testing"
)

func TestEISAbsoluteRegionBoundaryAndDeviceConflict(t *testing.T) {
	s := &eiSender{pointers: []eiPointerDevice{
		{device: 11, pointer: 111, regions: []eiRegion{{x: 10, y: 20, width: 100, height: 50, scale: 1}}},
		{device: 22, pointer: 222, regions: []eiRegion{{x: 110, y: 20, width: 100, height: 50, scale: 1}}},
	}}
	for _, tc := range []struct {
		x, y   int
		device uint64
		valid  bool
	}{
		{10, 20, 11, true}, {109, 69, 11, true}, {110, 20, 22, true}, {209, 69, 22, true},
		{9, 20, 0, false}, {10, 19, 0, false}, {109, 70, 0, false}, {210, 69, 0, false},
		{-1, 20, 0, false}, {10, -1, 0, false}, {math.MaxInt, 20, 0, false},
	} {
		p, ok := s.pointerForPoint(tc.x, tc.y)
		if ok != tc.valid || ok && p.device != tc.device {
			t.Errorf("point (%d,%d) got device %d valid %v, wanted device %d valid %v", tc.x, tc.y, p.device, ok, tc.device, tc.valid)
		}
	}
	s.pointers = append(s.pointers, eiPointerDevice{device: 33, pointer: 333, regions: []eiRegion{{x: 90, y: 30, width: 40, height: 20, scale: 1}}})
	if s.validatePoint(100, 40) {
		t.Fatal("overlap on competing devices was accepted")
	}
	if !s.validatePoint(10, 20) {
		t.Fatal("nonoverlapping region was wrongly rejected")
	}
}

func TestEISPointerRegionRandomBoundaryProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(99812))
	for i := 0; i < 3000; i++ {
		x, y := rng.Intn(1000), rng.Intn(1000)
		w, h := rng.Intn(500)+1, rng.Intn(500)+1
		s := &eiSender{pointers: []eiPointerDevice{{device: 1, pointer: 5, regions: []eiRegion{{x: uint32(x), y: uint32(y), width: uint32(w), height: uint32(h), scale: 1}}}}}
		for _, px := range []int{x - 1, x, x + w/2, x + w - 1, x + w} {
			for _, py := range []int{y - 1, y, y + h/2, y + h - 1, y + h} {
				want := px >= x && px < x+w && py >= y && py < y+h
				if got := s.validatePoint(px, py); got != want {
					t.Fatalf("%d: region (%d,%d) %dx%d point (%d,%d) got %t want %t", i, x, y, w, h, px, py, got, want)
				}
			}
		}
	}
}

func TestEISPointerFrameRejectsInexactFloat32Coordinates(t *testing.T) {
	s := &eiSender{client: &eiClient{}, pointers: []eiPointerDevice{{device: 1, pointer: 5, regions: []eiRegion{{x: 0, y: 0, width: 1 << 26, height: 500, scale: 1}}}}}
	for _, tc := range []struct {
		x       int
		wantErr bool
	}{{0, false}, {1 << 24, false}, {1<<24 + 1, true}, {(1 << 24) + 2, false}} {
		_, err := eiPointerFrame(s, tc.x, 100)
		if (err != nil) != tc.wantErr {
			t.Errorf("x=%d err=%v wantErr=%t", tc.x, err, tc.wantErr)
		}
	}
}
