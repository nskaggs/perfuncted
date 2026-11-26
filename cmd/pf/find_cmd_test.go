package main

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/pftest"
	"image"
	"image/color"
	"os"
	"strings"
	"testing"
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

// Every find command needs a real session before it can observe screen data.
// A failed open must never be converted to a successful match, pixel, or hash;
// retrying a failed open within the command would also duplicate authorization.
func TestFindCommandsRejectFailedSessionOpenWithoutPublishingResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"wait for", []string{"find", "wait-for", "--hash", "0x12"}},
		{"wait for change", []string{"find", "wait-for-change", "--initial", "0x12"}},
		{"wait for change capture", []string{"find", "wait-for-change", "--capture-initial"}},
		{"wait for no change", []string{"find", "wait-for-no-change"}},
		{"scan for", []string{"find", "scan-for", "--rects", "0,0,8,8", "--wants", "0x12"}},
		{"wait for visible change", []string{"find", "wait-for-visible-change"}},
		{"color", []string{"find", "color", "--color", "0c2238"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			failure := errors.New("synthetic session authorization refused")
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { attempts++; return nil, failure }
			})
			if attempts != 1 || exit == 0 || !strings.Contains(stderr, failure.Error()) || stdout != "" {
				t.Fatalf("failed find session args=%v attempts=%d exit=%d stdout=%q stderr=%q", tc.args, attempts, exit, stdout, stderr)
			}
		})
	}
}

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
