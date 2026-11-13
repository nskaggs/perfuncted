package main

import (
	"context"
	"image"
	"image/color"
	"os"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// The visual-change command has two independent polling stages: first find
// a changed frame, then optionally ensure that it settles. A failure in
// either stage must not publish a partial success or silently keep polling.
func TestFindVisibleChangeDoesNotPublishPartialHashAfterLateCaptureFailure(t *testing.T) {
	before := pftest.SolidImage(8, 8, color.RGBA{R: 1, A: 255})
	after := pftest.SolidImage(8, 8, color.RGBA{G: 1, A: 255})
	for _, tc := range []struct {
		name      string
		frames    []image.Image
		stable    string
		wantReads int
	}{
		{"change observation denied", []image.Image{before}, "1", 2},
		{"stabilization denied after observed change", []image.Image{before, after}, "3", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			stdout, stderr, exit := captureRunIO(t, []string{"find", "wait-for-visible-change", "--rect", "0,0,8,8", "--stable", tc.stable, "--poll", "1ms", "--timeout", "200ms"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{GrabFunc: func(_ context.Context, _ image.Rectangle) (image.Image, error) {
						reads++
						if reads <= len(tc.frames) {
							return tc.frames[reads-1], nil
						}
						return nil, os.ErrPermission
					}}, nil, nil, nil), nil
				}
			})
			if exit == 0 || stdout != "" || !strings.Contains(stderr, "permission denied") || reads != tc.wantReads {
				t.Fatalf("late denied frame accepted: exit=%d stdout=%q stderr=%q reads=%d want=%d", exit, stdout, stderr, reads, tc.wantReads)
			}
		})
	}
}
