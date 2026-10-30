package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
)

func TestScreenPixelActionsRefuseMixedScaleMultiOutputMosaics(t *testing.T) {
	cases := []struct {
		name string
		outs []output.Info
	}{
		{"negative origin mixed scaling", []output.Info{
			{Name: "left", Available: true, Geometry: output.Geometry{X: -2560, Y: 0, W: 2560, H: 1440}, ResolutionW: 3840, ResolutionH: 2160, ScaleNumerator: 3, ScaleDenominator: 2},
			{Name: "right", Available: true, Geometry: output.Geometry{X: 0, Y: 0, W: 1920, H: 1080}, ResolutionW: 1920, ResolutionH: 1080, Scale: 1},
		}},
		{"disjoint outputs", []output.Info{
			{Name: "top", Available: true, Geometry: output.Geometry{X: 0, Y: -1200, W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080, ScaleNumerator: 6, ScaleDenominator: 5},
			{Name: "bottom", Available: true, Geometry: output.Geometry{X: 0, Y: 240, W: 2560, H: 1440}, ResolutionW: 3840, ResolutionH: 2160, ScaleNumerator: 3, ScaleDenominator: 2},
		}},
		{"side by side same scale", []output.Info{
			{Name: "first", Available: true, Geometry: output.Geometry{X: 0, Y: 0, W: 1600, H: 900}, ResolutionW: 1600, ResolutionH: 900, Scale: 1},
			{Name: "second", Available: true, Geometry: output.Geometry{X: 1600, Y: 0, W: 1600, H: 900}, ResolutionW: 1600, ResolutionH: 900, Scale: 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
			outs := &outputTestLister{outs: tc.outs}
			_, stderr, code := captureRunIO(t, []string{"input", "click-screen", "--x", "700", "--y", "400"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
				}
			})
			if code == 0 || len(in.Calls) != 0 {
				t.Fatalf("unsupported output mosaic was accepted: code=%d stderr=%q calls=%v", code, stderr, in.Calls)
			}
		})
	}
}

func TestScreenPixelActionFailsClosedAfterHotplugChangesTopology(t *testing.T) {
	in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
	outs := &outputTestLister{outs: []output.Info{
		{Name: "first", Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080, ScaleNumerator: 6, ScaleDenominator: 5},
	}}
	run := func() (string, int) {
		_, stderr, code := captureRunIO(t, []string{"input", "click-screen", "--x", "960", "--y", "540"}, func(*cliConfig) sessionOpener {
			return func(context.Context) (*perfuncted.Session, error) {
				return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
			}
		})
		return stderr, code
	}
	if stderr, code := run(); code != 0 {
		t.Fatalf("single monitor should be supported: code=%d stderr=%q", code, stderr)
	}
	baseline := len(in.Calls)
	if baseline != 1 || !strings.Contains(in.Calls[0], "800,450") {
		t.Fatalf("wrong single-monitor mapping: %v", in.Calls)
	}
	outs.outs = append(outs.outs, output.Info{
		Name: "hotplugged", Available: true, Geometry: output.Geometry{X: -1920, Y: 0, W: 1920, H: 1080},
		ResolutionW: 1920, ResolutionH: 1080, Scale: 1,
	})
	if stderr, code := run(); code == 0 {
		t.Fatalf("hotplugged display should invalidate screen coordinate conversion: %q", stderr)
	}
	if len(in.Calls) != baseline {
		t.Fatalf("invalid post-hotplug action produced input: %v", in.Calls)
	}
	outs.outs = outs.outs[:1]
	if stderr, code := run(); code != 0 {
		t.Fatalf("restored single output must remap safely: %q", stderr)
	}
	if len(in.Calls) != baseline+1 {
		t.Fatalf("recovered output did not accept valid mapped click: %v", in.Calls)
	}
}
