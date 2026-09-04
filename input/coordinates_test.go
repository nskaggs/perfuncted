package input

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
)

func TestCoordinateSpaceInfoValidate(t *testing.T) {
	tests := []struct {
		name    string
		info    CoordinateSpaceInfo
		wantErr bool
	}{
		{name: "logical", info: CoordinateSpaceInfo{Kind: CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}},
		{name: "physical with authoritative scale", info: CoordinateSpaceInfo{Kind: CoordinateSpacePhysical, ScaleX: 2, ScaleY: 1.5}},
		{name: "unknown kind", info: CoordinateSpaceInfo{Kind: "browser-dpr", ScaleX: 2, ScaleY: 2}, wantErr: true},
		{name: "zero scale", info: CoordinateSpaceInfo{Kind: CoordinateSpacePhysical, ScaleX: 0, ScaleY: 1}, wantErr: true},
		{name: "non-finite scale", info: CoordinateSpaceInfo{Kind: CoordinateSpacePhysical, ScaleX: math.Inf(1), ScaleY: 1}, wantErr: true},
		{name: "logical scale is never inferred", info: CoordinateSpaceInfo{Kind: CoordinateSpaceLogical, ScaleX: 2, ScaleY: 2}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.info.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestWlVirtualBackendPointerCoordinateSpaceIsLogical(t *testing.T) {
	info, err := (&WlVirtualBackend{}).PointerCoordinateSpace(context.Background())
	if err != nil {
		t.Fatalf("PointerCoordinateSpace: %v", err)
	}
	if info.Kind != CoordinateSpaceLogical || info.ScaleX != 1 || info.ScaleY != 1 {
		t.Fatalf("coordinate space = %+v, want global logical 1x1", info)
	}
	if err := info.Validate(); err != nil {
		t.Fatalf("reported coordinate space invalid: %v", err)
	}
}

func TestNilWlVirtualBackendPointerCoordinateSpaceFailsClosed(t *testing.T) {
	var backend *WlVirtualBackend
	_, err := backend.PointerCoordinateSpace(context.Background())
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("nil backend error = %v, want ErrNotSupported", err)
	}
}

func TestBackendOperationCoordinateSpaceAdvertisement(t *testing.T) {
	if !slices.Contains((&WlVirtualBackend{}).SupportedOperations(), "pointer-coordinate-space") {
		t.Fatal("Wayland virtual backend did not advertise pointer-coordinate-space")
	}
	if slices.Contains((&XTestBackend{}).SupportedOperations(), "pointer-coordinate-space") {
		t.Fatal("XTest backend advertised unproven pointer-coordinate-space")
	}
	if slices.Contains((&UinputBackend{}).SupportedOperations(), "pointer-coordinate-space") {
		t.Fatal("uinput backend advertised unproven pointer-coordinate-space")
	}
}
