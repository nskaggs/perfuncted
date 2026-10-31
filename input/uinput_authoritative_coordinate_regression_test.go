package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/output"
	"slices"
	"testing"
)

func TestUinputReportsLogicalCoordinatesOnlyAfterSingleOutputDiscovery(t *testing.T) {
	oldOpen, oldCreate := openUinputOutputLister, newUinputBackend
	t.Cleanup(func() { openUinputOutputLister = oldOpen; newUinputBackend = oldCreate })
	cases := []struct {
		name       string
		outputs    []output.Info
		w, h       int32
		advertised bool
	}{
		{name: "authoritative single fractional output", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}, advertised: true},
		{name: "multiple outputs remain unproven", outputs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}}, {Available: true, Geometry: output.Geometry{X: 1600, W: 1280, H: 720}}}, advertised: false},
		{name: "explicit dimensions without authoritative output", w: 1600, h: 900, advertised: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			openUinputOutputLister = func(context.Context, env.Runtime) (output.Lister, error) {
				return &fakeUinputOutputLister{outputs: tc.outputs}, nil
			}
			newUinputBackend = func(int32, int32) (Inputter, error) { return &UinputBackend{}, nil }
			backend, err := newResolvedUinputBackend(context.Background(), env.Runtime{}, tc.w, tc.h)
			if err != nil {
				t.Fatal(err)
			}
			b, ok := backend.(*UinputBackend)
			if !ok {
				t.Fatalf("unexpected Uinput backend %T", backend)
			}
			coordinates, err := b.PointerCoordinateSpace(context.Background())
			if tc.advertised {
				if err != nil || coordinates.Kind != CoordinateSpaceLogical || coordinates.ScaleX != 1 || coordinates.ScaleY != 1 {
					t.Fatalf("coordinates=%+v err=%v", coordinates, err)
				}
			} else if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unproven coordinate mapping was advertised: %+v err=%v", coordinates, err)
			}
			if got := slices.Contains(b.SupportedOperations(), "pointer-coordinate-space"); got != tc.advertised {
				t.Fatalf("operations advertisement=%v want=%v", got, tc.advertised)
			}
		})
	}
}
