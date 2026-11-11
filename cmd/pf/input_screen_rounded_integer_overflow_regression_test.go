package main

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
)

// math.MaxInt is rounded upward when converted to float64 on 64-bit Go.
// A coordinate equal to that rounded limit must not pass the comparison
// and then wrap into a negative pointer coordinate during int conversion.
func TestClickScreenRefusesCoordinatesAtRoundedIntMaximumBeforeInjection(t *testing.T) {
	for _, tc := range []struct {
		name           string
		scaleX, scaleY float64
	}{
		{"horizontal", math.Exp2(63), 1},
		{"vertical", 1, math.Exp2(63)},
		{"both", math.Exp2(63), math.Exp2(63)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: tc.scaleX, ScaleY: tc.scaleY}}
			outs := &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 100, H: 100}, ResolutionW: 100, ResolutionH: 100}}}
			out, stderr, exit := captureRunIO(t, []string{"input", "click-screen", "--x=1", "--y=1"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
				}
			})
			if exit == 0 || len(in.Calls) != 0 || !strings.Contains(stderr, "outside pointer range") {
				t.Fatalf("overflow was dispatched: exit=%d stdout=%q stderr=%q calls=%v", exit, out, stderr, in.Calls)
			}
		})
	}
}
