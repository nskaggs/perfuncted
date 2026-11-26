//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/screen"
)

func certifyReleaseWindowCapture(t *testing.T, ctx context.Context, s *suite, binary string, target *perfuncted.Window) {
	t.Helper()
	info, err := target.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want, err := s.pf.Screen().CaptureWindow(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	metaPath := filepath.Join(dir, "metadata.json")
	for _, selector := range [][]string{{"--window-id", info.StableID()}, {"--window-title", info.Title}} {
		args := append([]string{"screen", "grab", "--out", "-", "--metadata", metaPath}, selector...)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("window PNG: %v: %s", err, stderr.String())
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(metaPath)
		if err != nil {
			t.Fatal(err)
		}
		var meta screen.WindowCapture
		if err := json.Unmarshal(data, &meta); err != nil {
			t.Fatal(err)
		}
		expected := image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H)
		if meta.WindowID != info.StableID() || meta.ScreenRect != expected || meta.ScreenRect != want.ScreenRect || meta.PixelBounds != want.PixelBounds || meta.Mode != screen.WindowCaptureVisibleScreenCrop || meta.PixelScaleX != want.PixelScaleX || meta.PixelScaleY != want.PixelScaleY || img.Bounds().Size() != meta.PixelBounds.Size() {
			t.Fatalf("wrong CLI provenance: %+v; expected %v, API=%+v", meta, expected, want)
		}
	}
	if s.mode == displayHeadlessWayland || s.mode == displayNestedWayland {
		output := filepath.Join(dir, "unsupported.png")
		metadata := filepath.Join(dir, "unsupported.json")
		cmd := exec.CommandContext(ctx, binary, "screen", "grab", "--window-id", info.StableID(), "--background", "--out", output, "--metadata", metadata)
		cmd.Env = s.rt.EnvList()
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("Wayland background capture incorrectly succeeded: %s", out)
		}
		for _, path := range []string{output, metadata} {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("unsupported background capture emitted %s: %v", path, err)
			}
		}
	}
}
