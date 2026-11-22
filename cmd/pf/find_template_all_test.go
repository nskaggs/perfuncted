package main

import (
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
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
