package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/pftest"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
