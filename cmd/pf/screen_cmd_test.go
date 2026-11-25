package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type screenCommandFailureWriter struct {
	bytes.Buffer
	failAt  int
	failure error
	writes  int
}

func (w *screenCommandFailureWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.failure
	}
	return w.Buffer.Write(p)
}

func runScreenCommandWithCapture(t *testing.T, capture *pftest.Screenshotter, out io.Writer, args ...string) error {
	t.Helper()
	cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) {
		return pftest.New(capture, nil, nil, nil), nil
	})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(out)
	cmd.SetArgs(args)
	return cmd.ExecuteContext(context.Background())
}

func TestScreenCommandsPreserveCaptureAndEncodingFailures(t *testing.T) {
	failure := errors.New("synthetic screenshot source failure")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"stdout grab", []string{"grab", "--out", "-"}},
		{"file grab", []string{"grab", "--out", t.TempDir() + "/capture.png"}},
		{"wait for predicate", []string{"wait-for-fn"}},
		{"wait for settle", []string{"wait-for-settle"}},
		{"multiple pixels", []string{"get-multiple-pixels", "--points", "0,0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runScreenCommandWithCapture(t, &pftest.Screenshotter{Err: failure}, io.Discard, tc.args...)
			if !errors.Is(err, failure) {
				t.Fatalf("screen command error = %v, want source failure", err)
			}
		})
	}
}

func TestGetAllPixelsRejectsUnexpectedCaptureTypeWithoutOutput(t *testing.T) {
	frame := image.NewGray(image.Rect(0, 0, 1, 1))
	var out bytes.Buffer
	err := runScreenCommandWithCapture(t, &pftest.Screenshotter{Frames: []image.Image{frame}}, &out, "get-all-pixels")
	if err == nil || !strings.Contains(err.Error(), "unexpected image type *image.Gray") {
		t.Fatalf("get-all-pixels error = %v, want concrete image type rejection", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected capture type emitted %d bytes", out.Len())
	}
}

func TestGetMultiplePixelsPreservesPlainAndJSONWriterFailures(t *testing.T) {
	failure := errors.New("synthetic output consumer failure")
	for _, tc := range []struct {
		name   string
		format string
	}{
		{"plain", "plain"},
		{"json", "json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &screenCommandFailureWriter{failAt: 1, failure: failure}
			err := runScreenCommandWithCapture(
				t,
				&pftest.Screenshotter{Width: 1, Height: 1},
				out,
				"get-multiple-pixels", "--points", "0,0", "--output", tc.format,
			)
			if !errors.Is(err, failure) || out.writes != 1 {
				t.Fatalf("screen output error=%v writes=%d, want one propagated write failure", err, out.writes)
			}
		})
	}
}

func TestScreenWatchPropagatesBackendAndOutputFailures(t *testing.T) {
	failure := errors.New("synthetic screen watch failure")
	t.Run("backend", func(t *testing.T) {
		err := runScreenCommandWithCapture(
			t,
			&pftest.Screenshotter{Err: failure},
			io.Discard,
			"watch", "--poll", "1ms", "--duration", "1s",
		)
		if !errors.Is(err, failure) {
			t.Fatalf("screen watch error = %v, want backend failure", err)
		}
	})

	for _, tc := range []struct {
		name   string
		format string
		failAt int
	}{
		{"initial plain result", "plain", 1},
		{"initial JSON result", "json", 1},
		{"changed plain result", "plain", 2},
		{"changed JSON result", "json", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &screenCommandFailureWriter{failAt: tc.failAt, failure: failure}
			frames := []image.Image{
				solidScreenCommandFrame(color.RGBA{A: 255}),
				solidScreenCommandFrame(color.RGBA{R: 255, A: 255}),
			}
			err := runScreenCommandWithCapture(
				t,
				&pftest.Screenshotter{Frames: frames},
				out,
				"watch", "--poll", "1ms", "--duration", "1s", "--output", tc.format,
			)
			if !errors.Is(err, failure) || out.writes != tc.failAt {
				t.Fatalf("screen watch output error=%v writes=%d, want failure at write %d", err, out.writes, tc.failAt)
			}
		})
	}
}

func solidScreenCommandFrame(c color.RGBA) image.Image {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, c)
	return frame
}

// A compositor may return an image that fails PNG encoding after capture.
// Output path publication must be all-or-nothing: failure must not truncate
// an existing file or leave a partial PNG at the requested destination.
func TestPNGGrabRegionKeepsExistingFileWhenEncodingFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.png")
	if err := os.WriteFile(target, []byte("retain original private screenshot"), 0600); err != nil {
		t.Fatal(err)
	}
	sc := &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rectangle{}), nil
	}}
	cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(sc, nil, nil, nil), nil })
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"grab-region", "--rect", "0,0,2,2", "--out", target})
	var out, errout bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errout)
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("invalid PNG capture was reported as success")
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil || string(data) != "retain original private screenshot" {
		t.Fatalf("failed PNG encoding truncated existing target: %v data=%q", readErr, data)
	}
	if strings.Contains(errout.String(), target) {
		t.Fatalf("failed PNG encoding published success destination: %q", errout.String())
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".perfuncted-capture-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("failed PNG encoding leaked temp files: %v %v", matches, err)
	}
}

func TestPNGGrabRegionPublishesCompleteImageAfterSuccessfulEncoding(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "capture.png")
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	frame.SetRGBA(1, 1, color.RGBA{R: 17, G: 28, B: 39, A: 255})
	calls := 0
	sc := &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) { calls++; return frame, nil }}
	cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(sc, nil, nil, nil), nil })
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs([]string{"grab-region", "--rect", "0,0,2,2", "--out", target})
	var output, diagnostics bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&diagnostics)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("file publication captured %d times", calls)
	}
	if output.Len() != 0 || !strings.Contains(diagnostics.String(), target) {
		t.Fatalf("wrong CLI publication receipt: stdout=%q stderr=%q", output.String(), diagnostics.String())
	}
	f, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	decoded, err := png.Decode(f)
	if err != nil {
		t.Fatalf("published invalid PNG: %v", err)
	}
	got := color.RGBAModel.Convert(decoded.At(1, 1)).(color.RGBA) //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
	if got != (color.RGBA{R: 17, G: 28, B: 39, A: 255}) {
		t.Fatalf("published image pixel changed: %+v", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".perfuncted-capture-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary capture remained: %v %v", matches, err)
	}
}

// Reject malformed capture buffers before PNG encoding. Encoding untrusted
// image.RGBA can read outside Pix, or emit PNG headers before discovering an
// unusable capture; neither a crash nor a partial image is a valid response.
func TestPNGScreenCaptureRejectsTruncatedProviderBufferBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{
		{"RGBA short last row", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"RGBA overlapping rows", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 4, Pix: make([]byte, 12)}},
		{"NRGBA short last row", &image.NRGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) { return tc.frame, nil }}
			cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(sc, nil, nil, nil), nil })
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"grab-region", "--rect", "0,0,2,2", "--out", "-"})
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("malformed PNG source accepted: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("invalid capture emitted %d PNG bytes before validation", out.Len())
			}
		})
	}
}

// The screen-settle poller reuses one timer across captures. A previously
// expired timer must not wake the next polling interval immediately, and a
// stopped timer must remain reusable without a stale notification.
func TestScreenPollTimerStopDrainAllowsSafeReuse(t *testing.T) {
	const poll = 15 * time.Millisecond
	for _, scenario := range []string{"active", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			timer := time.NewTimer(time.Hour)
			defer timer.Stop()
			if scenario == "expired" {
				if !timer.Stop() {
					t.Fatal("fresh long timer already expired")
				}
				timer.Reset(time.Nanosecond)
				select {
				case <-timer.C:
				case <-time.After(time.Second):
					t.Fatal("expired timer notification missing")
				}
			}
			stopAndDrainTimer(timer)
			for attempt := 0; attempt < 4; attempt++ {
				started := time.Now()
				timer.Reset(poll)
				select {
				case <-timer.C:
					if elapsed := time.Since(started); elapsed < poll/2 {
						t.Fatalf("stale timer wakeup after %s (expected interval %s)", elapsed, poll)
					}
				case <-time.After(time.Second):
					t.Fatal("reused screen polling timer never fired")
				}
				stopAndDrainTimer(timer)
			}
		})
	}
}

// A malformed local flag must never trigger desktop authorization, including
// a portal consent prompt or a compositor-backed capture session.
func TestScreenInvalidLocalArgumentsNeverOpenDesktopSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"grab rectangle", []string{"screen", "grab", "--rect", "malformed"}},
		{"hash rectangle", []string{"screen", "hash", "--rect", "malformed"}},
		{"grab region rectangle", []string{"screen", "grab-region", "--rect", "malformed"}},
		{"watch rectangle", []string{"screen", "watch", "--rect", "malformed"}},
		{"watch poll", []string{"screen", "watch", "--poll", "not-a-duration"}},
		{"watch duration", []string{"screen", "watch", "--duration", "not-a-duration"}},
		{"wait predicate rectangle", []string{"screen", "wait-for-fn", "--rect", "malformed"}},
		{"wait predicate name", []string{"screen", "wait-for-fn", "--predicate", "unknown-predicate"}},
		{"wait predicate poll", []string{"screen", "wait-for-fn", "--poll", "not-a-duration"}},
		{"wait predicate timeout", []string{"screen", "wait-for-fn", "--timeout", "not-a-duration"}},
		{"wait settle rectangle", []string{"screen", "wait-for-settle", "--rect", "malformed"}},
		{"wait settle samples", []string{"screen", "wait-for-settle", "--stable", "invalid"}},
		{"wait settle poll", []string{"screen", "wait-for-settle", "--poll", "not-a-duration"}},
		{"wait settle timeout", []string{"screen", "wait-for-settle", "--timeout", "not-a-duration"}},
		{"multiple pixels empty", []string{"screen", "get-multiple-pixels", "--points", ""}},
		{"multiple pixels format", []string{"screen", "get-multiple-pixels", "--points", "1,2", "--output", "unknown"}},
		{"multiple pixels coordinates", []string{"screen", "get-multiple-pixels", "--points", "one,two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			cmd := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("unexpected screen authorization")
				}
			})
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			cmd.SetArgs(tc.args)
			err := cmd.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("invalid input was accepted")
			}
			if opens != 0 {
				t.Fatalf("invalid screen flags opened %d desktop sessions: %v", opens, err)
			}
			if out.Len() != 0 {
				t.Fatalf("invalid invocation published data: %q", out.String())
			}
		})
	}
}

// The raw RGBA writer is a public CLI output boundary. Validate the complete
// image layout before streaming any pixels, even when a screenshot provider
// bypasses higher-level image validation.
func TestScreenRawWriterRejectsMissingAndTruncatedFramesBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame *image.RGBA
		want  string
	}{
		{"missing image", nil, "nil capture"},
		{"truncated backing buffer", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 8)}, "invalid RGBA frame stride"},
		{"invalid negative stride", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: -8, Pix: make([]byte, 16)}, "invalid RGBA frame stride"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := writePackedRGBAPixels(&stdout, tc.frame)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("malformed frame accepted: %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("malformed frame leaked %d bytes", stdout.Len())
			}
		})
	}
}

// No output metadata is not evidence of a particular monitor resolution.
// The caller must continue to the authorized screenshot fallback.
func TestScreenOutputResolutionProbeWithoutBundleMustFallBack(t *testing.T) {
	w, h, ok := singleOutputPhysicalResolution(context.Background(), nil)
	if ok || w != 0 || h != 0 {
		t.Fatalf("missing output bundle fabricated geometry: %dx%d ok=%t", w, h, ok)
	}
}

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

func TestScreenWatchCancellationDuringCaptureExitsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc := &pftest.Screenshotter{GrabFunc: func(ctx context.Context, _ image.Rectangle) (image.Image, error) {
		cancel()
		return nil, ctx.Err()
	}}
	stdout, stderr, code := captureRunIOContext(ctx, t,
		[]string{"screen", "watch", "--rect", "0,0,2,2", "--poll", "1ms", "--output", "json"},
		func(*cliConfig) sessionOpener {
			return func(context.Context) (*perfuncted.Session, error) {
				return pftest.New(sc, nil, nil, nil), nil
			}
		})
	if code != 0 {
		t.Fatalf("canceled screen watch exited %d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestSyntheticScreenCaptureCommandsWriteRealImageAndPixels(t *testing.T) {
	pixels := pftest.SolidImage(8, 8, color.RGBA{R: 12, G: 34, B: 56, A: 255})
	for _, tc := range []struct {
		name string
		args []string
		want string
		path bool
	}{
		{"region hash", []string{"screen", "hash", "--rect", "0,0,8,8"}, fmt.Sprintf("%08x", find.PixelHash(pixels, nil)), false},
		{"pixel", []string{"screen", "pixel", "--x", "4", "--y", "3"}, "R=12 G=34 B=56", false},
		{"resolution", []string{"screen", "resolution"}, "8x8", false},
		{"grab", []string{"screen", "grab", "--rect", "0,0,8,8"}, "", true},
		{"grab region", []string{"screen", "grab-region", "--rect", "0,0,8,8"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path {
				tc.args = append(tc.args, "--out", filepath.Join(t.TempDir(), "screen.png"))
			}
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{Frames: []image.Image{pixels}}, nil, nil, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("screenshot command %v failed code=%d out=%q err=%q", tc.args, code, out, stderr)
			}
			if tc.path {
				p := tc.args[len(tc.args)-1]
				f, err := os.Open(p)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				decoded, err := png.Decode(f)
				if err != nil {
					t.Fatalf("capture is not PNG: %v", err)
				}
				if decoded.Bounds().Dx() != 8 || decoded.Bounds().Dy() != 8 {
					t.Fatalf("wrong PNG geometry: %v", decoded.Bounds())
				}
				r, g, b, _ := decoded.At(3, 4).RGBA()
				if r>>8 != 12 || g>>8 != 34 || b>>8 != 56 {
					t.Fatalf("corrupted screenshot pixel: %d %d %d", r>>8, g>>8, b>>8)
				}
			}
		})
	}
}
func TestSyntheticScreenCommandsRejectCaptureFailures(t *testing.T) {
	for _, args := range [][]string{{"screen", "hash"}, {"screen", "pixel"}, {"screen", "grab-region", "--rect", "0,0,8,8", "--out", "-"}, {"screen", "get-all-pixels"}, {"find", "color", "--color", "0c2238", "--rect", "0,0,8,8"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{Err: os.ErrPermission}, nil, nil, nil), nil
				}
			})
			if code == 0 || strings.Contains(out, "0x") || !strings.Contains(stderr, "permission denied") {
				t.Fatalf("failed capture unexpectedly succeeded code=%d out=%q err=%q", code, out, stderr)
			}
		})
	}
}
func TestSyntheticFindCLIHashAndColorMatches(t *testing.T) {
	pixels := pftest.SolidImage(8, 8, color.RGBA{R: 12, G: 34, B: 56, A: 255})
	hash := find.PixelHash(pixels, nil)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"find color", []string{"find", "color", "--color", "0c2238", "--rect", "0,0,8,8"}, "0,0"},
		{"wait hash", []string{"find", "wait-for", "--rect", "0,0,8,8", "--hash", fmt.Sprintf("0x%x", hash), "--poll", "1ms", "--timeout", "150ms"}, fmt.Sprintf("%08x", hash)},
		{"stable hash", []string{"find", "wait-for-no-change", "--rect", "0,0,8,8", "--stable", "2", "--poll", "1ms", "--timeout", "150ms"}, fmt.Sprintf("%08x", hash)},
		{"multi rect scan", []string{"find", "scan-for", "--rects", "0,0,8,8", "--wants", fmt.Sprintf("0x%x", hash), "--poll", "1ms", "--timeout", "150ms"}, "match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{Frames: []image.Image{pixels}}, nil, nil, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("matching command %v code=%d out=%q err=%q want=%q", tc.args, code, out, stderr, tc.want)
			}
		})
	}
}

func TestSyntheticScreenAndFindCommandsObserveActualFrameChanges(t *testing.T) {
	before := pftest.SolidImage(8, 8, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	after := pftest.SolidImage(8, 8, color.RGBA{R: 20, G: 30, B: 40, A: 255})
	initialHash := find.PixelHash(before, nil)
	changedHash := find.PixelHash(after, nil)
	for _, tc := range []struct {
		name   string
		args   []string
		want   string
		images []image.Image
	}{
		{"find wait for change", []string{"find", "wait-for-change", "--rect", "0,0,8,8", "--initial", fmt.Sprintf("0x%x", initialHash), "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{after}},
		{"find change after capture", []string{"find", "wait-for-change", "--rect", "0,0,8,8", "--capture-initial", "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{before, after}},
		{"find visible change", []string{"find", "wait-for-visible-change", "--rect", "0,0,8,8", "--stable", "1", "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{before, after}},
		{"find stable after change", []string{"screen", "wait-for-settle", "--rect", "0,0,8,8", "--stable", "2", "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{before, after}},
		{"nonempty predicate", []string{"screen", "wait-for-fn", "--rect", "0,0,8,8", "--predicate", "non-empty", "--poll", "1ms", "--timeout", "200ms"}, fmt.Sprintf("%08x", changedHash), []image.Image{after}},
		{"multiple pixels plain", []string{"screen", "get-multiple-pixels", "--points", "1,2;3,4"}, "R=20 G=30 B=40 A=255", []image.Image{after}},
		{"multiple pixels JSON", []string{"screen", "get-multiple-pixels", "--points", "1,2;3,4", "--output", "json"}, `"x":1`, []image.Image{after}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{Frames: tc.images}, nil, nil, nil), nil
				}
			})
			if code != 0 || !strings.Contains(out, tc.want) {
				t.Fatalf("changing frames command %v: code=%d stdout=%q stderr=%q want %q", tc.args, code, out, stderr, tc.want)
			}
		})
	}
}

func TestSyntheticScreenAndFindRejectInvalidInputsBeforeAnyCapture(t *testing.T) {
	cases := [][]string{
		{"find", "wait-for", "--rect", "not-a-rect", "--hash", "0x1234"},
		{"find", "wait-for", "--rect", "0,0,8,8", "--hash", "invalid"},
		{"find", "wait-for-change", "--rect", "bad", "--initial", "0x12"},
		{"find", "wait-for-change", "--rect", "0,0,8,8", "--initial", "invalid"},
		{"find", "wait-for-no-change", "--rect", "bad"},
		{"find", "scan-for", "--rects", "bad", "--wants", "0x1234"},
		{"find", "scan-for", "--rects", "0,0,8,8", "--wants", "nothex"},
		{"find", "scan-for", "--rects", "0,0,8,8;1,1,2,2", "--wants", "0x1234"},
		{"find", "color", "--rect", "0,0,8,8", "--color", "not-hex"},
		{"screen", "wait-for-fn", "--rect", "0,0,8,8", "--predicate", "invalid"},
		{"screen", "wait-for-settle", "--rect", "0,0,8,8", "--stable", "not-a-count"},
		{"screen", "get-multiple-pixels", "--points", "not-coordinates"},
		{"screen", "get-multiple-pixels", "--points", "1,2", "--output", "garbage"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			calls := 0
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.New(&pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) {
						calls++
						return pftest.SolidImage(8, 8, color.RGBA{A: 255}), nil
					}}, nil, nil, nil), nil
				}
			})
			if code == 0 || stderr == "" || strings.Contains(out, "match") {
				t.Fatalf("invalid input %v was accepted: stdout=%q stderr=%q code=%d", args, out, stderr, code)
			}
			// Some invalid output-mode configurations are checked after collecting a
			// valid sample; all other rejected forms must not reach the transport.
			if calls != 0 && (len(args) <= 1 || args[1] != "get-multiple-pixels" || !strings.Contains(strings.Join(args, " "), "garbage")) {
				t.Fatalf("invalid argument reached screenshot transport %v: %d grabs", args, calls)
			}
		})
	}
}

type cancelingPredicateImage struct {
	bounds image.Rectangle
	calls  int
	cancel context.CancelFunc
}

func (img *cancelingPredicateImage) ColorModel() color.Model { return color.RGBAModel }
func (img *cancelingPredicateImage) Bounds() image.Rectangle { return img.bounds }
func (img *cancelingPredicateImage) At(_, _ int) color.Color {
	img.calls++
	if img.cancel != nil && img.calls == 5 {
		img.cancel()
	}
	return color.RGBA{}
}

// The screen wait predicate is an application cancellation boundary. A long
// transparent screenshot must not continue scanning all remaining pixels
// after its wait was canceled, nor accept a successful match after cancel.
func TestScreenPredicateStopsScanningOnCancellation(t *testing.T) {
	for _, name := range []string{"non-empty", "non-zero", "opaque"} {
		t.Run(name, func(t *testing.T) {
			predicate, err := screenPredicate(name)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			img := &cancelingPredicateImage{bounds: image.Rect(0, 0, 900, 900), cancel: cancel}
			if predicate(ctx, img) {
				t.Fatal("empty image matched")
			}
			if img.calls > 900 {
				t.Fatalf("predicate continued after cancellation: %d pixel reads", img.calls)
			}
			cancel()
			img.calls = 0
			img.cancel = nil
			if predicate(ctx, img) {
				t.Fatal("canceled predicate matched")
			}
			if img.calls != 0 {
				t.Fatalf("predicate read %d pixels after pre-cancellation", img.calls)
			}
		})
	}
}

func TestScreenPredicateStillFindsVisiblePixelsWhenNotCanceled(t *testing.T) {
	for _, tc := range []struct {
		name string
		rgba color.RGBA
		want bool
	}{
		{"non-empty", color.RGBA{R: 50, A: 255}, true},
		{"non-zero", color.RGBA{R: 50, A: 255}, true},
		{"opaque", color.RGBA{R: 50, A: 255}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			predicate, err := screenPredicate(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			img := image.NewRGBA(image.Rect(-3, -4, 4, 5))
			img.SetRGBA(3, 4, tc.rgba)
			if got := predicate(context.Background(), img); got != tc.want {
				t.Fatalf("visible pixel observed=%t want=%t", got, tc.want)
			}
		})
	}
}

func rawScreenshotCommand(t *testing.T, frame image.Image) ([]byte, error) {
	t.Helper()
	command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
		return pftest.New(&pftest.Screenshotter{Frames: []image.Image{frame}}, nil, nil, nil), nil
	})
	var output bytes.Buffer
	command.SilenceUsage = true
	command.SilenceErrors = true
	command.SetOut(&output)
	command.SetArgs([]string{"get-all-pixels"})
	err := command.ExecuteContext(context.Background())
	return bytes.Clone(output.Bytes()), err
}

// Row padding is not pixel data; it can contain uninitialized or sensitive
// backing-buffer bytes. The CLI promises exactly width*height*4 RGBA bytes.
func TestRawScreenshotOutputOmitsProviderRowPaddingAndTrailingBytes(t *testing.T) {
	frame := &image.RGBA{Rect: image.Rect(20, 30, 22, 32), Stride: 12, Pix: []byte{
		1, 2, 3, 4, 5, 6, 7, 8, 0xf1, 0xf2, 0xf3, 0xf4,
		9, 10, 11, 12, 13, 14, 15, 16, 0xe1, 0xe2, 0xe3, 0xe4,
	}}
	got, err := rawScreenshotCommand(t, frame)
	if err != nil {
		t.Fatalf("raw capture: %v", err)
	}
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if !bytes.Equal(got, want) {
		t.Fatalf("raw stream disclosed nonpixel bytes or wrong stride: got %x want %x", got, want)
	}
}

func TestRawScreenshotRejectsMalformedProviderBuffersBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame *image.RGBA
	}{
		{"truncated row", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"invalid stride", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 4, Pix: make([]byte, 8)}},
		{"negative stride", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: -8, Pix: make([]byte, 16)}},
		{"huge width overflow", &image.RGBA{Rect: image.Rect(0, 0, math.MaxInt, 2), Stride: 8, Pix: make([]byte, 16)}},
		{"inverted bounds", &image.RGBA{Rect: image.Rectangle{Min: image.Pt(10, 0), Max: image.Pt(1, 2)}, Stride: 8, Pix: make([]byte, 16)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := rawScreenshotCommand(t, tc.frame)
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("malformed provider buffer accepted: %v", err)
			}
			if len(got) > 0 {
				t.Fatalf("malformed provider buffer emitted %d bytes", len(got))
			}
		})
	}
}

// A valid subimage may omit trailing row padding. The final row only needs
// enough backing bytes for its actual pixels, not another complete stride.
func TestRawScreenshotAcceptsMinimalFinalRowOfPaddedSubimage(t *testing.T) {
	frame := &image.RGBA{Rect: image.Rect(9, 12, 11, 14), Stride: 12, Pix: []byte{
		1, 2, 3, 4, 5, 6, 7, 8, 0xfa, 0xfb, 0xfc, 0xfd,
		9, 10, 11, 12, 13, 14, 15, 16,
	}}
	got, err := rawScreenshotCommand(t, frame)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	if !bytes.Equal(got, want) {
		t.Fatalf("valid padded subimage bytes %x, want %x", got, want)
	}
}

// A watch canceled after session setup but before its first sample must exit
// cleanly without capturing pixels or writing a misleading initial sample.
func TestScreenWatchCanceledBeforeInitialSampleDoesNotCapture(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	samples := 0
	opens := 0
	capture := &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) {
		samples++
		t.Fatal("canceled watch tried to sample private pixels")
		return nil, context.Canceled
	}}
	out, errOut, code := captureRunIOContext(ctx, t, []string{"screen", "watch", "--rect", "0,0,2,2", "--poll", "1ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opens++
			return pftest.New(capture, nil, nil, nil), nil
		}
	})
	if code != 0 || opens != 1 || samples != 0 || out != "" {
		t.Fatalf("canceled watch code=%d opens=%d samples=%d stdout=%q stderr=%q", code, opens, samples, out, errOut)
	}
}

type backgroundCLIScreenshotter struct {
	*pftest.Screenshotter
	capture func(context.Context, string) (screen.WindowPixmapCapture, error)
}

func (s *backgroundCLIScreenshotter) CaptureWindowPixmap(ctx context.Context, id string) (screen.WindowPixmapCapture, error) {
	return s.capture(ctx, id)
}

func TestScreenGrabBackgroundUsesTargetPixmapAndReportsOcclusion(t *testing.T) {
	info := window.Info{NativeID: "42", Title: "Editor", PID: 42, X: 10, Y: 20, W: 20, H: 10}
	mgr := &pftest.Manager{Lists: [][]window.Info{{info}, {info}, {info}}}
	backend := &backgroundCLIScreenshotter{
		Screenshotter: &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) {
			t.Fatal("background capture must not fall back to a visible screen crop")
			return nil, nil
		}},
		capture: func(_ context.Context, id string) (screen.WindowPixmapCapture, error) {
			if id != info.NativeID {
				t.Fatalf("target ID=%q, want %q", id, info.NativeID)
			}
			return screen.WindowPixmapCapture{Image: image.NewRGBA(image.Rect(0, 0, 20, 10)),
				ScreenRect: image.Rect(10, 20, 30, 30), Visibility: screen.WindowPixmapOccluded}, nil
		},
	}
	command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
		return pftest.New(backend, nil, mgr, nil), nil
	})
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"grab", "--window-id", "42", "--background", "--out", "-"})
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%q)", err, stderr.String())
	}
	if _, err := png.Decode(bytes.NewReader(stdout.Bytes())); err != nil {
		t.Fatalf("decode target PNG: %v", err)
	}
	if !strings.Contains(stderr.String(), "mode=background-window-pixmap") || !strings.Contains(stderr.String(), "state=occluded") {
		t.Fatalf("background provenance report=%q", stderr.String())
	}
	if len(mgr.Activated) != 0 {
		t.Fatalf("activated windows=%v", mgr.Activated)
	}
}

func TestScreenGrabSelectsWindowByStableIDTitleAndPID(t *testing.T) {
	first := window.Info{NativeID: "editor-one", Title: "Editor", PID: 41, X: -5, Y: 10, W: 30, H: 20}
	second := window.Info{NativeID: "editor-two", Title: "Editor", PID: 42, X: 50, Y: 60, W: 20, H: 15}
	for _, tc := range []struct {
		name     string
		args     []string
		windows  []window.Info
		wantRect image.Rectangle
	}{
		{name: "stable id disambiguates duplicate title", args: []string{"grab", "--window-id", "editor-two", "--out", "-"}, windows: []window.Info{first, second}, wantRect: image.Rect(50, 60, 70, 75)},
		{name: "exact title", args: []string{"grab", "--window-title", "Editor", "--out", "-"}, windows: []window.Info{first}, wantRect: image.Rect(-5, 10, 25, 30)},
		{name: "pid", args: []string{"grab", "--pid", "42", "--out", "-"}, windows: []window.Info{first, second}, wantRect: image.Rect(50, 60, 70, 75)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			windows := tc.windows
			mgr := &pftest.Manager{Lists: [][]window.Info{windows, windows, windows, windows}}
			var rect image.Rectangle
			grabs := 0
			sc := &pftest.Screenshotter{GrabFunc: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
				rect = requested
				grabs++
				return image.NewRGBA(image.Rect(0, 0, requested.Dx(), requested.Dy())), nil
			}}
			command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
				return pftest.New(sc, nil, mgr, nil), nil
			})
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs(tc.args)
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			if err := command.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("execute: %v (stderr=%q)", err, stderr.String())
			}
			if grabs != 1 {
				t.Fatalf("captures=%d, want one", grabs)
			}
			if rect != tc.wantRect {
				t.Fatalf("capture rect=%v, want %v", rect, tc.wantRect)
			}
			if len(mgr.Activated) != 0 {
				t.Fatalf("activated windows=%v", mgr.Activated)
			}
			if !bytes.HasPrefix(stdout.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
				t.Fatalf("stdout is not a PNG: %q", stdout.Bytes()[:min(stdout.Len(), 32)])
			}
			if _, err := png.Decode(bytes.NewReader(stdout.Bytes())); err != nil {
				t.Fatalf("decode stdout PNG: %v", err)
			}
			if !strings.Contains(stderr.String(), "mode=visible-screen-crop") || !strings.Contains(stderr.String(), "screen_rect=") {
				t.Fatalf("missing separate capture report: %q", stderr.String())
			}
		})
	}
}

func TestScreenGrabWindowWritesPNGAndSeparateMetadata(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "window.png")
	metadataPath := filepath.Join(dir, "window.json")
	info := window.Info{NativeID: "editor", Title: "Editor", AppID: "org.example.Editor", PID: 42, X: 10, Y: 20, W: 24, H: 12}
	windows := []window.Info{info}
	mgr := &pftest.Manager{Lists: [][]window.Info{windows, windows, windows}}
	sc := &pftest.Screenshotter{GrabFunc: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, requested.Dx(), requested.Dy())), nil
	}}
	command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
		return pftest.New(sc, nil, mgr, nil), nil
	})
	command.SilenceErrors = true
	command.SilenceUsage = true
	command.SetArgs([]string{"grab", "--window-title", "Editor", "--out", imagePath, "--metadata", metadataPath})
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v (stderr=%q)", err, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != imagePath {
		t.Fatalf("stdout=%q, want image path", stdout.String())
	}
	file, err := os.Open(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(file); err != nil {
		_ = file.Close()
		t.Fatalf("published invalid PNG: %v", err)
	}
	_ = file.Close()
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata perfuncted.WindowCapture
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.WindowID != "editor" || metadata.ScreenRect != image.Rect(10, 20, 34, 32) || metadata.Mode != "visible-screen-crop" {
		t.Fatalf("metadata=%+v", metadata)
	}
	if metadata.PixelBounds != image.Rect(0, 0, 24, 12) || metadata.PixelScaleX != 1 || metadata.PixelScaleY != 1 {
		t.Fatalf("pixel provenance=%+v", metadata)
	}
	if metadata.Occlusion == "" || metadata.GeometrySource != "window-manager-reported" {
		t.Fatalf("incomplete provenance metadata=%+v", metadata)
	}
}

func TestScreenGrabWindowRejectsAmbiguityAndSelectorConflictsBeforeCapture(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		list [][]window.Info
	}{
		{name: "duplicate exact title", args: []string{"grab", "--window-title", "Editor", "--out", "-"}, list: [][]window.Info{{
			{NativeID: "one", Title: "Editor", X: 1, Y: 2, W: 5, H: 5},
			{NativeID: "two", Title: "Editor", X: 8, Y: 2, W: 5, H: 5},
		}}},
		{name: "PID with multiple windows", args: []string{"grab", "--pid", "42", "--out", "-"}, list: [][]window.Info{{
			{NativeID: "one", Title: "Editor", PID: 42, X: 1, Y: 2, W: 5, H: 5},
			{NativeID: "two", Title: "Dialog", PID: 42, X: 8, Y: 2, W: 5, H: 5},
		}}},
		{name: "multiple selectors", args: []string{"grab", "--window-id", "one", "--pid", "42"}},
		{name: "window and rect", args: []string{"grab", "--window-id", "one", "--rect", "1,2,6,7"}},
		{name: "background requires window selector", args: []string{"grab", "--background"}},
		{name: "background selector cannot use rect", args: []string{"grab", "--window-id", "one", "--background", "--rect", "1,2,6,7"}},
		{name: "metadata without selector", args: []string{"grab", "--metadata", "capture.json"}},
		{name: "metadata stdout", args: []string{"grab", "--window-id", "one", "--metadata", "-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens, grabs := 0, 0
			mgr := &pftest.Manager{Lists: tc.list}
			sc := &pftest.Screenshotter{GrabFunc: func(_ context.Context, requested image.Rectangle) (image.Image, error) {
				grabs++
				return image.NewRGBA(image.Rect(0, 0, requested.Dx(), requested.Dy())), nil
			}}
			command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return pftest.New(sc, nil, mgr, nil), nil
			})
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs(tc.args)
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			err := command.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("invalid or ambiguous selector was accepted")
			}
			if tc.name != "duplicate exact title" && tc.name != "PID with multiple windows" && opens != 0 {
				t.Fatalf("opened session %d times before selector validation", opens)
			}
			if grabs != 0 {
				t.Fatalf("captured %d times for rejected selector", grabs)
			}
			if len(mgr.Activated) != 0 {
				t.Fatalf("activated windows=%v", mgr.Activated)
			}
		})
	}
}

func TestScreenGrabMetadataCannotOverwritePNG(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "image.png")
	if err := os.WriteFile(imagePath, []byte("existing image"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadataAlias := filepath.Join(dir, "metadata.json")
	if err := os.Symlink(imagePath, metadataAlias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		imagePath    string
		metadataPath string
	}{
		{name: "same lexical path", imagePath: "same.png", metadataPath: "./same.png"},
		{name: "same file through symlink", imagePath: imagePath, metadataPath: metadataAlias},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			command := screenCmd(func(context.Context) (*perfuncted.Session, error) {
				opens++
				return nil, nil
			})
			command.SilenceErrors = true
			command.SilenceUsage = true
			command.SetArgs([]string{"grab", "--window-id", "id", "--out", tc.imagePath, "--metadata", tc.metadataPath})
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			if err := command.ExecuteContext(context.Background()); err == nil {
				t.Fatal("same PNG and metadata path was accepted")
			}
			if opens != 0 {
				t.Fatalf("opened session %d times before path-conflict validation", opens)
			}
		})
	}
}

// The vertical axis must reject a logical coordinate beyond the platform int
// range even when the horizontal axis maps correctly. Returning either partial
// coordinate could send an input event to an unrelated window.
func TestScreenshotLogicalVerticalOverflowRejectsEntirePoint(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin int
		pixel  int
	}{
		{name: "positive overflow", origin: math.MaxInt, pixel: 1},
		{name: "negative underflow", origin: math.MinInt, pixel: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := []output.Info{{Available: true, Geometry: output.Geometry{X: 100, Y: tc.origin, W: 3, H: 3}, ResolutionW: 3, ResolutionH: 3}}
			x, y, err := screenshotPointToLogical(info, 1, tc.pixel)
			if tc.origin == math.MinInt {
				// Pixel 0 is exactly the valid minimum coordinate.
				if err != nil || x != 101 || y != math.MinInt {
					t.Fatalf("minimum valid origin: (%d,%d), err=%v", x, y, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "out of range") || x != 0 || y != 0 {
				t.Fatalf("vertical overflow produced (%d,%d), err=%v; must reject whole point", x, y, err)
			}
		})
	}
}
