package main

import (
	"context"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"strings"
	"testing"
	"time"
)

func TestScreenshotPointMapsToLogicalFractionalScale(t *testing.T) {
	outs := []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}
	x, y, err := screenshotPointToLogical(outs, 1200, 600)
	if err != nil || x != 1000 || y != 500 {
		t.Fatalf("mapped %d,%d err=%v, want 1000,500", x, y, err)
	}
	for _, bad := range [][]output.Info{{}, {{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 0, ResolutionH: 0}}, {outs[0], outs[0]}} {
		if _, _, err := screenshotPointToLogical(bad, 100, 100); err == nil {
			t.Errorf("accepted ambiguous/unknown output geometry %+v", bad)
		}
	}
}

func TestVerifiedActivationObservesTargetWindowNotJustInputDelivery(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "A", Active: false}}, {{NativeID: "one", Title: "A", Active: true}}}}
	pf := pftest.New(nil, nil, m, nil)
	defer pf.Close()
	w, err := pf.Windows.Find(context.Background(), window.Match{TitleExact: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWindowActive(context.Background(), w, 75*time.Millisecond, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	m = &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "A", Active: false}}}}
	pf2 := pftest.New(nil, nil, m, nil)
	defer pf2.Close()
	w, err = pf2.Windows.Find(context.Background(), window.Match{TitleExact: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if err := waitWindowActive(context.Background(), w, 20*time.Millisecond, 5*time.Millisecond); err == nil {
		t.Fatal("delivered event incorrectly considered verified activation")
	}
}

func TestWaylandXTestInfoExplicitlyWarnsAboutNativeWindows(t *testing.T) {
	report := infoReport{Compositor: "KDE Plasma Wayland", Capabilities: map[string]capabilityEntry{"input": {Supported: true, Backend: "*input.XTestBackend"}}}
	if msg := inputBackendWarning(report); !strings.Contains(msg, "XWayland") || !strings.Contains(msg, "remote-desktop") {
		t.Fatalf("warning=%q", msg)
	}
	report.Compositor = "X11"
	if msg := inputBackendWarning(report); msg != "" {
		t.Errorf("native X11 should not warn, got %q", msg)
	}
}

func TestClickWindowCLIRejectsMissingTargetBeforeOpening(t *testing.T) {
	opened := false
	_, _, code := captureRunIO(t, []string{"input", "click-window", "--x", "15", "--y", "20"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { opened = true; return nil, nil }
	})
	if code == 0 || opened {
		t.Fatalf("missing target code=%d opened=%v", code, opened)
	}
}

var _ = input.CoordinateSpaceLogical
