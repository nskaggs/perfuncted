package input

import (
	"errors"
	"testing"
)

// A disconnected or unbound pointer does not own its advertised region.
// A valid authorized pointer must remain selectable even when stale EIS
// device metadata advertises an overlapping region.
func TestEISUnboundPointerCannotMaskLiveOverlappingPointer(t *testing.T) {
	for _, tc := range []struct {
		name string
		dead eiPointerDevice
	}{
		{"unbound device", eiPointerDevice{device: 0, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}},
		{"unbound pointer", eiPointerDevice{device: 501, pointer: 0, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := eiPointerDevice{device: 201, pointer: 202, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}
			for _, pointers := range [][]eiPointerDevice{{tc.dead, live}, {live, tc.dead}} {
				sender := &eiSender{pointers: pointers}
				pointer, ok := sender.pointerForPoint(25, 30)
				if !ok || pointer.device != live.device || pointer.pointer != live.pointer {
					t.Fatalf("stale overlapping pointer masked live authorization: got=%+v ok=%v", pointer, ok)
				}
			}
		})
	}
}

func TestEISPointerRegionWithoutBoundDeviceNeverAuthorizesCoordinates(t *testing.T) {
	for _, pointer := range []eiPointerDevice{
		{device: 0, pointer: 202, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}},
		{device: 201, pointer: 0, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}},
	} {
		sender := &eiSender{pointers: []eiPointerDevice{pointer}}
		if sender.validatePoint(25, 30) {
			t.Fatalf("unbound pointer accepted coordinate authorization %+v", pointer)
		}
		if sender.hasAuthorizedPointer() {
			t.Fatalf("unbound pointer advertised capability %+v", pointer)
		}
		if _, err := eiPointerFrame(sender, 25, 30); err == nil || errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("unbound pointer frame result %v", err)
		}
	}
}
