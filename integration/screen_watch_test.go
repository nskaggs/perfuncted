//go:build integration

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
	"time"
)

func runScreenWatchSmoke(t *testing.T, s *suite) {
	t.Helper()
	switch s.mode {
	case displayHeadlessWayland:
		if s.rt.SocketPath() == "" {
			t.Fatal("screen watch requires the managed headless Wayland socket")
		}
	case displayHeadlessX11:
		if s.rt.Get("DISPLAY") == "" || s.rt.Get("WAYLAND_DISPLAY") != "" {
			t.Fatal("screen watch requires the managed headless X11 display")
		}
	default:
		t.Fatal("screen watch requires a managed headless session")
	}
	binary := filepath.Join(t.TempDir(), "pf")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-buildvcs=false", "-o", binary, "../cmd/pf")
	build.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pf: %v: %s", err, output)
	}
	wantScreen := image.Rect(10, 20, 42, 44)
	wantImage := wantScreen
	// X11 captures are region-local; Wayland preserves the requested origin.
	if s.mode == displayHeadlessX11 {
		wantImage = image.Rect(0, 0, wantScreen.Dx(), wantScreen.Dy())
	}
	for _, includePixels := range []bool{false, true} {
		name := "metadata"
		if includePixels {
			name = "pixels"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			args := []string{"screen", "watch", "--changes", "--output", "json", "--rect", "10,20,42,44", "--duration", "1s", "--poll", "100ms"}
			if includePixels {
				args = append(args, "--include-pixels")
			}
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Env = s.rt.EnvList()
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("screen watch: %v: %s", err, stderr.String())
			}
			var frame struct {
				FrameID         uint64            `json:"frame_id"`
				GeometryVersion uint64            `json:"geometry_version"`
				BaseFrameID     *uint64           `json:"base_frame_id"`
				ScreenRect      image.Rectangle   `json:"screen_rect"`
				ImageBounds     image.Rectangle   `json:"image_bounds"`
				FullFrame       bool              `json:"full_frame"`
				DirtyRects      []image.Rectangle `json:"dirty_rects"`
				Pixels          json.RawMessage   `json:"pixels"`
			}
			line, _, _ := bytes.Cut(output, []byte{'\n'})
			if err := json.Unmarshal(line, &frame); err != nil {
				t.Fatalf("decode initial NDJSON frame: %v; stderr: %s", err, stderr.String())
			}
			if !frame.FullFrame || frame.FrameID != 1 || frame.GeometryVersion != 1 || frame.BaseFrameID != nil ||
				frame.ScreenRect != wantScreen || frame.ImageBounds != wantImage ||
				len(frame.DirtyRects) != 1 || frame.DirtyRects[0] != wantScreen {
				t.Fatalf("unexpected initial frame geometry or provenance: %+v", frame)
			}
			if !includePixels {
				if frame.Pixels != nil {
					t.Fatal("metadata-only frame contains pixels field")
				}
				return
			}
			var pixels []struct {
				Encoding        string          `json:"encoding"`
				ImageBounds     image.Rectangle `json:"image_bounds"`
				Width           int             `json:"width"`
				Height          int             `json:"height"`
				PixelScale      float64         `json:"pixel_scale"`
				GeometryVersion uint64          `json:"geometry_version"`
				Data            []byte          `json:"data"`
			}
			if err := json.Unmarshal(frame.Pixels, &pixels); err != nil || len(pixels) != 1 {
				t.Fatalf("expected one full-frame pixel payload: count=%d err=%v", len(pixels), err)
			}
			p := pixels[0]
			if p.Encoding != "png+base64" || p.ImageBounds != wantImage ||
				p.Width != wantImage.Dx() || p.Height != wantImage.Dy() ||
				p.PixelScale != 1 || p.GeometryVersion != frame.GeometryVersion {
				t.Fatal("unexpected pixel payload geometry or encoding")
			}
			img, err := png.Decode(bytes.NewReader(p.Data))
			if err != nil {
				t.Fatalf("decode PNG: %v", err)
			}
			if img.Bounds() != image.Rect(0, 0, p.Width, p.Height) {
				t.Fatalf("PNG dimensions: got %v, want %dx%d", img.Bounds(), p.Width, p.Height)
			}
		})
	}
}
