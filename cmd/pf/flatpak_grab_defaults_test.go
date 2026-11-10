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

func TestGrabDestinationPreservesNativePathButStreamsInFlatpak(t *testing.T) {
	for _, tc := range []struct {
		path    string
		flatpak bool
		want    string
	}{
		{"", false, "/tmp/pf-grab.png"},
		{"", true, "-"},
		{"-", false, "-"},
		{"-", true, "-"},
		{"/shared/screenshot.png", true, "/shared/screenshot.png"},
		{"relative.png", false, "relative.png"},
	} {
		if got := grabDestination(tc.path, tc.flatpak); got != tc.want {
			t.Errorf("destination(%q,%t)=%q want %q", tc.path, tc.flatpak, got, tc.want)
		}
	}
}

func TestGrabExplicitStdoutIsOnlyPNGAndUsesCapturedPixels(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 3, 2))
	frame.SetRGBA(1, 1, color.RGBA{R: 13, G: 27, B: 49, A: 255})
	screen := &pftest.Screenshotter{GrabFunc: func(context.Context, image.Rectangle) (image.Image, error) { return frame, nil }}
	cmd := screenCmd(func(context.Context) (*perfuncted.Session, error) { return pftest.New(screen, nil, nil, nil), nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"grab", "--rect", "0,0,3,2", "--out", "-"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("binary screenshot wrote diagnostic noise: %q", stderr.String())
	}
	img, err := png.Decode(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("invalid stdout PNG: %v", err)
	}
	if img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 {
		t.Fatalf("screenshot dimensions: %v", img.Bounds())
	}
	got := color.RGBAModel.Convert(img.At(1, 1)).(color.RGBA) //nolint:errcheck // Color model guarantees RGBA
	if got != (color.RGBA{R: 13, G: 27, B: 49, A: 255}) {
		t.Fatalf("screenshot pixel changed: %+v", got)
	}
}

func TestGrabBinaryOutputRejectsCharacterDevicesButAllowsFiles(t *testing.T) {
	tty, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("no test character device: %v", err)
	}
	defer tty.Close()
	if err := refuseBinaryTerminal(tty); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("terminal-like device accepted binary screenshot: %v", err)
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "capture.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := refuseBinaryTerminal(file); err != nil {
		t.Fatalf("regular file refused: %v", err)
	}
	if err := refuseBinaryTerminal(&bytes.Buffer{}); err != nil {
		t.Fatalf("capturing stdout refused: %v", err)
	}
}
