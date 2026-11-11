//go:build linux

package input

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestUinputPointerTargetFailClosedBoundaryMatrix(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	closed := &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		backend  *UinputBackend
		ctx      context.Context
		x, y     int
		want     string
		canceled bool
	}{
		{name: "nil backend", want: "unavailable"},
		{name: "closed backend", backend: closed, want: "closed"},
		{name: "unknown bounds", backend: &UinputBackend{}, want: "bounds unavailable"},
		{name: "width one", backend: &UinputBackend{logicalWidth: 1, logicalHeight: 1080}, want: "bounds unavailable"},
		{name: "positive x overflow", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, x: math.MaxInt, want: "int32 range"},
		{name: "negative y overflow", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, y: math.MinInt, want: "int32 range"},
		{name: "negative x", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, x: -1, want: "outside logical desktop bounds"},
		{name: "negative y", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, y: -1, want: "outside logical desktop bounds"},
		{name: "right boundary", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, x: 1920, want: "outside logical desktop bounds"},
		{name: "bottom boundary", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, y: 1080, want: "outside logical desktop bounds"},
		{name: "last valid pixel", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}, x: 1919, y: 1079},
		{name: "origin valid", backend: &UinputBackend{logicalWidth: 1920, logicalHeight: 1080}},
		{name: "cancellation precedes nil backend", ctx: canceled, canceled: true},
		{name: "cancellation precedes invalid bounds", backend: &UinputBackend{}, ctx: canceled, canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			err := tc.backend.ValidatePointerTarget(ctx, tc.x, tc.y)
			if tc.canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v want cancellation", err)
				}
				return
			}
			if tc.want == "" {
				if err != nil {
					t.Fatalf("valid point rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}
