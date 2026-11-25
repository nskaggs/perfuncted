//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/find"
)

// Exercise both public entry points with independent, known fixture geometry.
func certifyReleaseFindCLIAndGoOCR(t *testing.T, ctx context.Context, s *suite, binary string) {
	t.Helper()
	template := image.NewRGBA(image.Rect(0, 0, 10, 10))
	draw.Draw(template, template.Bounds(), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	path := filepath.Join(t.TempDir(), "red.png")
	var data bytes.Buffer
	if err := png.Encode(&data, template); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pf %v: %v: %s", args, err, diagnostic.String())
		}
		return string(out)
	}
	args := []string{"find", "image", "--template", path, "--rect", "40,180,110,210", "--confidence", "1"}
	first := "match x=52 y=190 width=10 height=10 confidence=1.000000\n"
	second := "match x=82 y=190 width=10 height=10 confidence=1.000000\n"
	if got := run(args...); got != first {
		t.Fatalf("CLI best template: %q", got)
	}
	if got := run(append(args, "--all", "--max-results", "2")...); got != first+second {
		t.Fatalf("CLI ordered templates: %q", got)
	}
	if got := run(append(args, "--all", "--max-results", "1")...); got != first {
		t.Fatalf("CLI capped template: %q", got)
	}
	provider, err := find.NewTesseractProvider("", "eng")
	if err != nil {
		t.Fatal(err)
	}
	area := image.Rect(40, 40, 460, 180)
	opts := find.TextOptions{Recognizer: provider, MaxResults: 4}
	got, err := s.pf.Screen.FindText(ctx, area, "HELLO", opts)
	if err != nil || len(got.Matches) != 1 || got.Provider != "tesseract" || got.Language != "eng" || got.SourceRect != area {
		t.Fatalf("Go OCR result: %+v, %v", got, err)
	}
	var cli find.TextResult
	if err := json.Unmarshal([]byte(run("find", "text", "--query", "HELLO", "--rect", "40,40,460,180", "--provider", "tesseract", "--output", "json")), &cli); err != nil {
		t.Fatal(err)
	}
	if len(cli.Matches) != 1 || got.Matches[0].Rect != cli.Matches[0].Rect || got.Matches[0].PixelRect != cli.Matches[0].PixelRect || got.Matches[0].Text != cli.Matches[0].Text {
		t.Fatalf("Go/CLI OCR geometry disagreement: Go=%+v CLI=%+v", got, cli)
	}
	noMatch, err := s.pf.Screen.FindText(ctx, area, "NO SUCH WORD", opts)
	if err != nil || len(noMatch.Matches) != 0 {
		t.Fatalf("OCR absence is not successful empty match: %+v %v", noMatch, err)
	}
	if got := strings.TrimSpace(run("find", "text", "--query", "NO SUCH WORD", "--rect", "40,40,460,180", "--provider", "tesseract")); got != "no match" {
		t.Fatalf("plain OCR absence: %q", got)
	}
	t.Logf("independent Go and CLI recognition agrees at %s", fmt.Sprint(cli.Matches[0].Rect))
}
