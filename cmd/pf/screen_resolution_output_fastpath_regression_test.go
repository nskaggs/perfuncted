package main

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
)

// A single confirmed output's physical resolution is already available without
// exposing screen pixels or asking the user to approve a screenshot portal.
func TestScreenResolutionUsesSingleConfirmedOutputWithoutCapture(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outputs []output.Info
		want    string
	}{
		{"physical pixels not logical coordinates", []output.Info{{Name: "eDP-1", Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}, "1920x1080\n"},
		{"multi-output falls back to capture", []output.Info{{Available: true, ResolutionW: 1920, ResolutionH: 1080}, {Available: true, ResolutionW: 1280, ResolutionH: 720}}, ""},
		{"missing pixel size falls back to capture", []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &pftest.Screenshotter{Err: errors.New("screenshot portal awaits consent")}
			stdout, stderr, code := captureRunIO(t, []string{"screen", "resolution"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.NewWithOutputs(sc, nil, nil, &fakeOutputLister{infos: tc.outputs}, nil), nil
				}
			})
			if tc.want != "" {
				if code != 0 || stdout != tc.want || stderr != "" {
					t.Fatalf("resolution used screenshot: code=%d stdout=%q stderr=%q", code, stdout, stderr)
				}
			} else if code == 0 || stdout != "" {
				t.Fatalf("ambiguous output did not fall back to capture: code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}
