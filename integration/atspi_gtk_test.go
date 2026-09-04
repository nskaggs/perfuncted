//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/internal/executil"
)

// TestGTKAccessibilityRepresentative keeps one lightweight GTK representative
// in the suite. It uses a managed display only and verifies a real AT-SPI
// action closes the dialog, with process exit as the independent outcome.
func TestGTKAccessibilityRepresentative(t *testing.T) {
	s := mustSuite(t)
	if !s.pf.Has(perfuncted.CapabilityAccessibility) {
		t.Skip("managed session has no AT-SPI capability")
	}
	if _, err := executil.LookPath("zenity"); err != nil {
		t.Skipf("zenity unavailable: %v", err)
	}
	app := appSpec{name: "zenity", launch: []string{"zenity", "--info", "--title=Perfuncted GTK", "--text=AT-SPI representative", "--ok-label=Confirm"}, extraEnv: []string{"GTK_MODULES=atk-bridge"}}
	cmd, err := launchApp(s.rt, app, app.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch zenity: %v", err)
	}
	t.Cleanup(func() { terminateCmd(cmd, 5*time.Second) })
	if _, err := waitForWindow(s.pf, "Perfuncted GTK", 20*time.Second); err != nil {
		t.Skipf("zenity window unavailable in managed compositor: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := findWindowInfo(s.pf, ctx, "Perfuncted GTK")
	if err != nil {
		t.Fatalf("find zenity window: %v", err)
	}
	scope, err := s.pf.Accessibility.AccessibilityWindow(ctx, accessibility.WindowTarget{ID: info.NativeID, Title: info.Title, PID: info.PID, AppID: info.AppID, Bounds: accessibility.Rect{X: info.X, Y: info.Y, Width: info.W, Height: info.H}, Active: info.Active})
	if err != nil {
		if errors.Is(err, accessibility.ErrNotFound) || errors.Is(err, accessibility.ErrUnsupported) {
			apps, appsErr := s.pf.Accessibility.Applications(ctx)
			t.Logf("AT-SPI correlation diagnostics: window=%+v apps=%+v appsErr=%v", info, apps, appsErr)
			t.Skipf("AT-SPI could not correlate zenity window: %v", err)
		}
		t.Fatalf("correlate zenity window: %v", err)
	}
	nodes, err := s.pf.Accessibility.Find(ctx, scope.Root, accessibility.Query{Role: "button"}, accessibility.SnapshotOptions{MaxDepth: 8, MaxNodes: 256, MaxTextBytes: 256})
	if err != nil || len(nodes) == 0 {
		t.Fatalf("find zenity action button: nodes=%+v err=%v", nodes, err)
	}
	if _, err := s.pf.Accessibility.InvokeDefaultAction(ctx, nodes[0].ID); err != nil {
		t.Fatalf("invoke zenity AT-SPI action: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("zenity did not exit after AT-SPI action: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("zenity remained open after AT-SPI action: %v", ctx.Err())
	}
}
