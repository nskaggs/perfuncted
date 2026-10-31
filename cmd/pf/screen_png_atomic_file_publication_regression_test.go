package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
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
	got := color.RGBAModel.Convert(decoded.At(1, 1)).(color.RGBA)
	if got != (color.RGBA{R: 17, G: 28, B: 39, A: 255}) {
		t.Fatalf("published image pixel changed: %+v", got)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".perfuncted-capture-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary capture remained: %v %v", matches, err)
	}
}
