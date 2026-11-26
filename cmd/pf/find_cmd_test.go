package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/find"
	"github.com/nskaggs/perfuncted/v2/pftest"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
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

func TestFindImageAllCLI(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	src := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	src.SetNRGBA(2, 0, color.NRGBA{R: 255, A: 255})
	path := writeTemplatePNG(t, ref)
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{src}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	opens := 0
	output, stderr, code := captureRunIO(t, []string{"find", "image", "--template", path, "--rect", "0,0,3,1", "--all", "--confidence", "1"}, templateCommandOpener(session, &opens))
	if code != 0 || opens != 1 || strings.Count(output, "match x=") != 2 || !strings.Contains(output, "match x=0") || !strings.Contains(output, "match x=2") {
		t.Fatalf("code=%d opens=%d out=%q err=%q", code, opens, output, stderr)
	}
	for _, args := range [][]string{
		{"find", "image", "--template", path, "--rect", "0,0,3,1", "--all", "--max-results", "0"},
		{"find", "image", "--template", path, "--rect", "0,0,3,1", "--all", "--max-results", "65"},
		{"find", "image", "--template", path, "--rect", "0,0,3,1", "--max-results", "2"},
	} {
		before := opens
		out, _, code := captureRunIO(t, args, templateCommandOpener(session, &opens))
		if code == 0 || opens != before || out != "" {
			t.Fatalf("args=%v code=%d opens=%d out=%q", args, code, opens, out)
		}
	}
}

type templateFailWriter struct{}

func (templateFailWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestFindImageAllCLIErrorPaths(t *testing.T) {
	ref := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	ref.SetNRGBA(0, 0, color.NRGBA{A: 255, R: 255})
	frame := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	frame.SetNRGBA(0, 0, color.NRGBA{A: 255, R: 255})
	path := writeTemplatePNG(t, ref)
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{frame}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	for _, args := range [][]string{
		{"find", "image", "--template", path, "--rect", "0,0,1,1", "--timeout", "invalid"},
	} {
		opens := 0
		_, _, code := captureRunIO(t, args, templateCommandOpener(session, &opens))
		if code == 0 || opens != 0 {
			t.Fatalf("args=%v code=%d opens=%d", args, code, opens)
		}
	}
	noSession := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return nil, os.ErrPermission }
	}
	_, _, code := captureRunIO(t, []string{"find", "image", "--template", path, "--rect", "0,0,1,1", "--all"}, noSession)
	if code == 0 {
		t.Fatal("open error swallowed")
	}
	missing := perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = missing.Close() })
	opens := 0
	out, _, code := captureRunIO(t, []string{"find", "image", "--template", path, "--rect", "0,0,1,1", "--all"}, templateCommandOpener(missing, &opens))
	if code == 0 || out != "" {
		t.Fatalf("all error code=%d out=%q", code, out)
	}
	cmd := findTemplateCmd(func(context.Context) (*perfuncted.Session, error) { return session, nil })
	cmd.SetArgs([]string{"--template", path, "--rect", "0,0,1,1", "--all"})
	cmd.SetOut(templateFailWriter{})
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write error=%v", err)
	}
}

func writeTemplatePNG(t *testing.T, img image.Image) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "template.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func templateCommandOpener(session *perfuncted.Session, calls *int) cliOpenFactory {
	return func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			(*calls)++
			return session, nil
		}
	}
}

type verifiedTemplateTestScreenshotter struct {
	*pftest.Screenshotter
}

func (s *verifiedTemplateTestScreenshotter) GrabVerified(ctx context.Context, rect image.Rectangle) (find.VerifiedTemplateCapture, error) {
	img, err := s.Grab(ctx, rect)
	if err != nil {
		return find.VerifiedTemplateCapture{}, err
	}
	return find.VerifiedTemplateCapture{Image: img, DesktopRect: rect, PixelScale: 1}, nil
}

func TestFindImageCommandLocatesPNGAndPrintsDesktopScore(t *testing.T) {
	template := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	template.SetNRGBA(0, 0, color.NRGBA{R: 20, G: 30, B: 40, A: 255})
	template.SetNRGBA(1, 0, color.NRGBA{R: 60, G: 70, B: 80, A: 255})
	template.SetNRGBA(0, 1, color.NRGBA{R: 90, G: 100, B: 110, A: 255})
	template.SetNRGBA(1, 1, color.NRGBA{R: 120, G: 130, B: 140, A: 255})
	frame := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			frame.SetNRGBA(3+x, 4+y, template.NRGBAAt(x, y))
		}
	}
	path := writeTemplatePNG(t, template)
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{frame}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	calls := 0
	out, stderr, code := captureRunIO(t, []string{
		"find", "image", "--template", path, "--rect", "0,0,8,8", "--confidence", "0.95",
	}, templateCommandOpener(session, &calls))
	if code != 0 || calls != 1 || !strings.Contains(out, "match x=3 y=4 width=2 height=2 confidence=1.000000") {
		t.Fatalf("find image failed: code=%d opens=%d stdout=%q stderr=%q", code, calls, out, stderr)
	}
}

func TestFindImageCommandDecodesAndValidatesBeforeOpeningSession(t *testing.T) {
	validTemplate := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	validTemplate.SetNRGBA(0, 0, color.NRGBA{R: 10, A: 255})
	validPath := writeTemplatePNG(t, validTemplate)
	invalidPath := filepath.Join(t.TempDir(), "invalid.png")
	if err := os.WriteFile(invalidPath, []byte("not a png"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "bad PNG", args: []string{"find", "image", "--template", invalidPath, "--rect", "0,0,8,8"}},
		{name: "low confidence", args: []string{"find", "image", "--template", validPath, "--rect", "0,0,8,8", "--confidence", "-0.1"}},
		{name: "high confidence", args: []string{"find", "image", "--template", validPath, "--rect", "0,0,8,8", "--confidence", "1.1"}},
		{name: "NaN confidence", args: []string{"find", "image", "--template", validPath, "--rect", "0,0,8,8", "--confidence", "NaN"}},
		{name: "infinite confidence", args: []string{"find", "image", "--template", validPath, "--rect", "0,0,8,8", "--confidence", "+Inf"}},
		{name: "invalid rect", args: []string{"find", "image", "--template", validPath, "--rect", "bad"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			calls := 0
			out, stderr, code := captureRunIO(t, tc.args, templateCommandOpener(session, &calls))
			if code == 0 || stderr == "" || calls != 0 || strings.Contains(out, "match x=") {
				t.Fatalf("invalid invocation reached session or printed match: code=%d opens=%d stdout=%q stderr=%q", code, calls, out, stderr)
			}
		})
	}
}

func TestFindImageCommandUsesSemiTransparentPNGMaskWeight(t *testing.T) {
	template := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	template.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	template.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 128})
	frame := image.NewRGBA(image.Rect(0, 0, 2, 1))
	frame.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	frame.SetRGBA(1, 0, color.RGBA{G: 205, A: 255})
	path := writeTemplatePNG(t, template)
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{frame}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	calls := 0
	out, stderr, code := captureRunIO(t, []string{
		"find", "image", "--template", path, "--rect", "0,0,2,1", "--confidence", "0.97",
	}, templateCommandOpener(session, &calls))
	wantScore := 1 - float64(128*50)/float64((255+128)*3*255)
	wantScoreText := fmt.Sprintf("confidence=%.6f", wantScore)
	if code != 0 || calls != 1 || !strings.Contains(out, wantScoreText) {
		t.Fatalf("semi-transparent PNG match: code=%d opens=%d stdout=%q stderr=%q want score %s", code, calls, out, stderr, wantScoreText)
	}
}

func TestFindImageCommandRejectsOversizedPNGBeforeOpeningSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxTemplatePNGBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	session := perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	calls := 0
	out, stderr, code := captureRunIO(t, []string{
		"find", "image", "--template", path, "--rect", "0,0,8,8",
	}, templateCommandOpener(session, &calls))
	if code == 0 || calls != 0 || !strings.Contains(stderr, "exceeds") || strings.Contains(out, "match x=") {
		t.Fatalf("oversized template was not rejected before session open: code=%d opens=%d stdout=%q stderr=%q", code, calls, out, stderr)
	}
}

func TestFindImageCommandRejectsPNGPixelLimitBeforeOpeningSession(t *testing.T) {
	template := image.NewNRGBA(image.Rect(0, 0, 2049, 1024))
	path := writeTemplatePNG(t, template)
	session := perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	calls := 0
	out, stderr, code := captureRunIO(t, []string{
		"find", "image", "--template", path, "--rect", "0,0,8,8",
	}, templateCommandOpener(session, &calls))
	if code == 0 || calls != 0 || !strings.Contains(stderr, "dimensions") || strings.Contains(out, "match x=") {
		t.Fatalf("oversized PNG dimensions reached session open: code=%d opens=%d stdout=%q stderr=%q", code, calls, out, stderr)
	}
}

func TestFindImageCommandPropagatesCaptureFailureWithoutCoordinates(t *testing.T) {
	template := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	template.SetNRGBA(0, 0, color.NRGBA{R: 20, A: 255})
	path := writeTemplatePNG(t, template)
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Err: os.ErrPermission}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	calls := 0
	out, stderr, code := captureRunIO(t, []string{
		"find", "image", "--template", path, "--rect", "0,0,8,8",
	}, templateCommandOpener(session, &calls))
	if code == 0 || calls != 1 || strings.Contains(out, "match x=") || !strings.Contains(stderr, "permission denied") {
		t.Fatalf("capture failure was hidden: code=%d opens=%d stdout=%q stderr=%q", code, calls, out, stderr)
	}
}

func TestFindImageCommandPreservesNotFoundError(t *testing.T) {
	template := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	template.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	path := writeTemplatePNG(t, template)
	frame := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{frame}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	calls := 0
	out, stderr, code := captureRunIO(t, []string{
		"find", "image", "--template", path, "--rect", "0,0,8,8", "--confidence", "0.99",
	}, templateCommandOpener(session, &calls))
	if code == 0 || calls != 1 || strings.Contains(out, "match x=") || !strings.Contains(stderr, find.ErrNotFound.Error()) {
		t.Fatalf("missing template did not fail cleanly: code=%d opens=%d stdout=%q stderr=%q", code, calls, out, stderr)
	}
}

type commandTextRecognizer struct{ words []find.OCRWord }

func (*commandTextRecognizer) Name() string     { return "fixture" }
func (*commandTextRecognizer) Language() string { return "eng" }
func (r *commandTextRecognizer) Recognize(context.Context, image.Image) ([]find.OCRWord, error) {
	return r.words, nil
}

func TestFindTextCLIJSONUsesExplicitProviderAndScreenCoordinates(t *testing.T) {
	region := image.Rect(0, 0, 4, 2)
	frame := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	provider := &commandTextRecognizer{words: []find.OCRWord{
		{Text: "Continue", PixelRect: image.Rect(0, 0, 2, 1), Page: 1, Block: 1, Paragraph: 1, Line: 1, Word: 1},
		{Text: "Continue", PixelRect: image.Rect(2, 1, 4, 2), Page: 1, Block: 1, Paragraph: 1, Line: 2, Word: 1},
	}}
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{frame}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	openCalls := 0
	open := func(context.Context) (*perfuncted.Session, error) { openCalls++; return session, nil }
	factory := func(executable, language string) (find.OCRRecognizer, error) {
		if executable != "" || language != "eng" {
			t.Fatalf("factory executable=%q language=%q", executable, language)
		}
		return provider, nil
	}
	cmd := findTextCmd(open, factory)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--query", "Continue", "--rect", "0,0,4,2", "--provider", "tesseract", "--output", "json"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got find.TextResult
	if err := json.Unmarshal(output.Bytes(), &got); err != nil {
		t.Fatalf("JSON %q: %v", output.String(), err)
	}
	if openCalls != 1 || got.Provider != "fixture" || got.SourceRect != region || len(got.Matches) != 2 || got.Matches[0].Rect != image.Rect(0, 0, 2, 1) || got.Matches[1].Rect != image.Rect(2, 1, 4, 2) {
		t.Fatalf("openCalls=%d result=%+v", openCalls, got)
	}
}

func TestFindTextCLIMissingProviderAndBadRequestsDoNotOpenSession(t *testing.T) {
	openCalls := 0
	open := func(context.Context) (*perfuncted.Session, error) {
		openCalls++
		return nil, errors.New("must not open")
	}
	missing := func(string, string) (find.OCRRecognizer, error) { return nil, find.ErrOCRProviderUnavailable }
	cmd := findTextCmd(open, missing)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--query", "Continue", "--rect", "0,0,1,1", "--provider", "tesseract"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); !errors.Is(err, find.ErrOCRProviderUnavailable) || openCalls != 0 || output.Len() != 0 {
		t.Fatalf("missing provider err=%v open=%d out=%q", err, openCalls, output.String())
	}

	cmd = findTextCmd(open, func(string, string) (find.OCRRecognizer, error) { return &commandTextRecognizer{}, nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--query", "Continue", "--rect", "0,0,1,1", "--provider", "remote"})
	if err := cmd.Execute(); err == nil || openCalls != 0 {
		t.Fatalf("unsupported provider err=%v open=%d", err, openCalls)
	}
}

func TestFindTextCLIPlainNoMatchIsSuccess(t *testing.T) {
	region := image.Rect(0, 0, 2, 2)
	session := perfuncted.NewSessionForTesting(&verifiedTemplateTestScreenshotter{Screenshotter: &pftest.Screenshotter{Frames: []image.Image{image.NewNRGBA(region)}}}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	provider := &commandTextRecognizer{}
	cmd := findTextCmd(func(context.Context) (*perfuncted.Session, error) { return session, nil }, func(string, string) (find.OCRRecognizer, error) { return provider, nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--query", "missing", "--rect", "0,0,2,2", "--provider", "tesseract"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := cmd.Execute(); err != nil || output.String() != "no match\n" {
		t.Fatalf("err=%v output=%q", err, output.String())
	}
}
