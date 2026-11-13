package main

import (
	"context"
	"image"
	"os"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

// A failed capture is never evidence of a match, change, or settled state.
// These paths must propagate the original screenshot failure without retries
// that could falsely transform an unavailable frame into a successful sample.
func TestFindCommandsPropagateCaptureFailureWithoutPublishingHashes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"wait for", []string{"find", "wait-for", "--rect", "0,0,8,8", "--hash", "0x1234", "--poll", "1ms", "--timeout", "200ms"}},
		{"wait for change", []string{"find", "wait-for-change", "--rect", "0,0,8,8", "--initial", "0x1234", "--poll", "1ms", "--timeout", "200ms"}},
		{"wait for stable", []string{"find", "wait-for-no-change", "--rect", "0,0,8,8", "--stable", "2", "--poll", "1ms", "--timeout", "200ms"}},
		{"scan for", []string{"find", "scan-for", "--rects", "0,0,8,8", "--wants", "0x1234", "--poll", "1ms", "--timeout", "200ms"}},
		{"visible change initial", []string{"find", "wait-for-visible-change", "--rect", "0,0,8,8", "--poll", "1ms", "--timeout", "200ms"}},
		{"find color", []string{"find", "color", "--rect", "0,0,8,8", "--color", "0c2238"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens, frames := 0, 0
			stdout, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(&pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) { frames++; return nil, os.ErrPermission }}, nil, nil, nil), nil
				}
			})
			if opens != 1 || frames != 1 || code == 0 || stdout != "" || !strings.Contains(stderr, "permission denied") {
				t.Fatalf("failed capture args=%v opens=%d frames=%d code=%d stdout=%q stderr=%q", tc.args, opens, frames, code, stdout, stderr)
			}
		})
	}
}

// CLI rectangle parsing must reject malformed regions before performing any
// screen reads, regardless of which find operation the caller selected.
func TestFindCommandsRejectMalformedRectangleBeforeCapture(t *testing.T) {
	for _, args := range [][]string{
		{"find", "wait-for", "--hash", "0x1234", "--rect", "invalid"},
		{"find", "wait-for-change", "--initial", "0x1234", "--rect", "invalid"},
		{"find", "wait-for-no-change", "--rect", "invalid"},
		{"find", "wait-for-visible-change", "--rect", "invalid"},
		{"find", "color", "--color", "0c2238", "--rect", "invalid"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			opens, frames := 0, 0
			stdout, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return pftest.New(&pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) { frames++; return nil, os.ErrPermission }}, nil, nil, nil), nil
				}
			})
			if opens != 1 || frames != 0 || code == 0 || stdout != "" || stderr == "" || strings.Contains(stderr, "permission denied") {
				t.Fatalf("malformed rect args=%v opens=%d frames=%d code=%d stdout=%q stderr=%q", args, opens, frames, code, stdout, stderr)
			}
		})
	}
}
