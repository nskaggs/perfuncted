package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
