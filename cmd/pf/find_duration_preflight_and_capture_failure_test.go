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

// Invalid timing controls are rejected before a find command observes any
// frame, even if the session has already been opened for that operation.
func TestFindCommandsRejectInvalidTimingBeforeScreenshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"wait hash invalid poll", []string{"find", "wait-for", "--hash", "0x12", "--poll", "invalid"}},
		{"wait hash invalid timeout", []string{"find", "wait-for", "--hash", "0x12", "--timeout", "invalid"}},
		{"wait change invalid poll", []string{"find", "wait-for-change", "--initial", "0x12", "--poll", "invalid"}},
		{"wait change invalid timeout", []string{"find", "wait-for-change", "--initial", "0x12", "--timeout", "invalid"}},
		{"stable wait invalid poll", []string{"find", "wait-for-no-change", "--poll", "invalid"}},
		{"stable wait invalid timeout", []string{"find", "wait-for-no-change", "--timeout", "invalid"}},
		{"scan invalid poll", []string{"find", "scan-for", "--rects", "0,0,8,8", "--wants", "0x12", "--poll", "invalid"}},
		{"scan invalid timeout", []string{"find", "scan-for", "--rects", "0,0,8,8", "--wants", "0x12", "--timeout", "invalid"}},
		{"visible wait invalid poll", []string{"find", "wait-for-visible-change", "--poll", "invalid"}},
		{"visible wait invalid timeout", []string{"find", "wait-for-visible-change", "--timeout", "invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens, grabs := 0, 0
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(&pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) {
						grabs++
						return pftest.SolidImage(8, 8, color.RGBA{A: 255}), nil
					}}, nil, nil, nil), nil
				}
			})
			if opens != 1 || grabs != 0 || exit == 0 || stdout != "" || stderr == "" {
				t.Fatalf("invalid timing %v opens=%d grabs=%d exit=%d stdout=%q stderr=%q", tc.args, opens, grabs, exit, stdout, stderr)
			}
		})
	}
}

// Capture-initial is an actual screenshot authorization, not a zero-hash
// fallback. Failed initial capture must not proceed to poll or print a hash.
func TestFindChangeCaptureInitialFailureDoesNotPretendToObserveChange(t *testing.T) {
	frames := 0
	stdout, stderr, exit := captureRunIO(t, []string{"find", "wait-for-change", "--capture-initial", "--rect", "0,0,8,8", "--poll", "1ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.New(&pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) { frames++; return nil, os.ErrPermission }}, nil, nil, nil), nil
		}
	})
	if exit == 0 || stdout != "" || !strings.Contains(stderr, "permission denied") || frames != 1 {
		t.Fatalf("initial capture failure acknowledged: exit=%d stdout=%q stderr=%q frames=%d", exit, stdout, stderr, frames)
	}
}
