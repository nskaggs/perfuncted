package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
)

// Diagnostic collection is not an exemption from caller cancellation. The
// snapshot APIs must not open or publish files for a revoked request.
func TestDiagnosticCaptureRejectsPreCanceledRequestWithoutFiles(t *testing.T) {
	manager := newHandleWindowManager()
	source := syntheticMalformedScreenshotter{frame: image.NewRGBA(image.Rect(0, 0, 2, 2))}
	session := NewSessionForTesting(source, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	for _, op := range []struct {
		name string
		run  func() error
	}{
		{"screenshot", func() error { return session.captureScreenshot(ctx, dir) }},
		{"window list", func() error { return session.captureWindows(ctx, dir) }},
		{"active window", func() error { return session.captureActiveWindow(ctx, dir) }},
	} {
		t.Run(op.name, func(t *testing.T) {
			if err := op.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled diagnostics returned %v", err)
			}
		})
	}
	for _, name := range []string{"screenshot.png", "windows.json", "active-window.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canceled diagnostics created %s: %v", name, err)
		}
	}
	child, stop := session.diagnosticContext(nil)
	defer stop()
	if child == nil || child.Err() != nil {
		t.Fatalf("nil diagnostic context did not produce a live bounded context: %v", child)
	}
	if _, ok := child.Deadline(); !ok {
		t.Fatal("diagnostic context has no timeout")
	}
}
