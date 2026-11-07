//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// Coordinate authority belongs to the live virtual input backend, not to
// constructor metadata left behind after device teardown. A closed backend
// must never report an active coordinate mapping.
func TestUinputClosedBackendDoesNotAdvertiseCoordinateAuthority(t *testing.T) {
	b := &UinputBackend{verifiedSingleLogicalOutput: true}
	before, err := b.PointerCoordinateSpace(context.Background())
	if err != nil || before.Kind != CoordinateSpaceLogical {
		t.Fatalf("live verified backend: %+v %v", before, err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := b.PointerCoordinateSpace(context.Background())
	if after != (CoordinateSpaceInfo{}) || !errors.Is(err, errUinputBackendClosed) {
		t.Fatalf("closed backend retained authority: %+v %v", after, err)
	}
}
