//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/screen"
	"github.com/nskaggs/perfuncted/v2/window"
)

func TestWindowCaptureIntegration(t *testing.T) {
	s := mustSuite(t)
	var app *appSpec
	for _, candidate := range requiredApps(t) {
		if !candidate.isBrowser {
			selected := candidate
			app = &selected
			break
		}
	}
	if app == nil {
		t.Skip("no supported editor application is installed")
	}
	app.saveFile = filepath.Join(t.TempDir(), app.name+"-window-capture.txt")
	if err := os.WriteFile(app.saveFile, nil, 0o600); err != nil {
		t.Fatalf("create temporary editor document: %v", err)
	}
	cmd, err := launchApp(s.rt, *app, app.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch %s: %v", app.name, err)
	}
	t.Cleanup(func() { terminateCmd(cmd, 5*time.Second) })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	target, err := s.pf.Windows().WithContext(ctx).Wait(window.Match{TitleContains: app.winMatch}, perfuncted.WaitEvery(250*time.Millisecond))
	if err != nil {
		t.Fatalf("find %s window: %v", app.name, err)
	}
	before, err := target.WithContext(ctx).Info()
	if err != nil {
		t.Fatalf("read %s window geometry: %v", app.name, err)
	}
	capture, err := s.pf.Screen().WithContext(ctx).CaptureWindow(target)
	if before.W <= 0 || before.H <= 0 {
		if !errors.Is(err, perfuncted.ErrUnsupported) {
			t.Fatalf("geometry-unavailable backend capture error=%v, want ErrUnsupported", err)
		}
		t.Logf("%s reports no window bounds; capture was rejected as unsupported", s.mode)
		return
	}
	if err != nil {
		t.Fatalf("capture %s window with geometry %v: %v", app.name, image.Rect(before.X, before.Y, before.X+before.W, before.Y+before.H), err)
	}
	want := image.Rect(before.X, before.Y, before.X+before.W, before.Y+before.H)
	if capture.WindowID != target.ID().String() || capture.ScreenRect != want || capture.Capture.ScreenRect != want {
		t.Fatalf("window capture metadata=%+v, want id=%q rect=%v", capture, target.ID().String(), want)
	}
	if capture.Capture.Image == nil || capture.Capture.Image.Bounds().Empty() {
		t.Fatalf("window capture returned empty pixels: %+v", capture.Capture.Image)
	}
	if capture.PixelScaleX <= 0 || capture.PixelScaleY <= 0 || capture.PixelScaleX != capture.PixelScaleY {
		t.Fatalf("capture scale=(%v,%v), want equal positive scale", capture.PixelScaleX, capture.PixelScaleY)
	}
	if capture.Mode != screen.WindowCaptureVisibleScreenCrop {
		t.Fatalf("capture mode=%q", capture.Mode)
	}
	t.Logf("%s captured window %q at %v with pixel scale (%v,%v)", s.mode, capture.WindowID, capture.ScreenRect, capture.PixelScaleX, capture.PixelScaleY)
	if info, infoErr := target.WithContext(ctx).Info(); infoErr != nil {
		t.Fatalf("refresh window after capture: %v", infoErr)
	} else if info.StableID() != before.StableID() {
		t.Fatalf("selected window changed after capture: %q -> %q", before.StableID(), info.StableID())
	}
}
