package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
)

type failingDiagnosticScreenshotBackend struct {
	syntheticMalformedScreenshotter
	failure error
}

func (b *failingDiagnosticScreenshotBackend) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	if b.failure != nil {
		return nil, b.failure
	}
	return b.syntheticMalformedScreenshotter.Grab(ctx, rect)
}

type failingDiagnosticWindowBackend struct {
	*handleWindowManager
	failure error
}

func (b *failingDiagnosticWindowBackend) List(ctx context.Context) ([]window.Info, error) {
	if b.failure != nil {
		return nil, b.failure
	}
	return b.handleWindowManager.List(ctx)
}
func (b *failingDiagnosticWindowBackend) ActiveTitle(context.Context) (string, error) {
	if b.failure != nil {
		return "", b.failure
	}
	return "recovered title", nil
}

type failingDiagnosticOutputBackend struct {
	failure error
}

func (b *failingDiagnosticOutputBackend) List(context.Context) ([]output.Info, error) {
	if b.failure != nil {
		return nil, b.failure
	}
	return []output.Info{{Name: "recovered-output"}}, nil
}
func (*failingDiagnosticOutputBackend) Close() error { return nil }

// Failed diagnostic reads must not publish incomplete private artifacts or
// overwrite an earlier capture. A subsequent healthy read may publish a new
// artifact without recreating the owning session.
func TestDiagnosticArtifactsRejectBackendTransportFailureAndRecover(t *testing.T) {
	failure := errors.New("synthetic provider transport failure")
	screen := &failingDiagnosticScreenshotBackend{syntheticMalformedScreenshotter: syntheticMalformedScreenshotter{frame: image.NewRGBA(image.Rect(0, 0, 2, 2))}, failure: failure}
	windows := &failingDiagnosticWindowBackend{handleWindowManager: newHandleWindowManager(window.Info{NativeID: "recovered", Title: "recovered title"}), failure: failure}
	outputs := &failingDiagnosticOutputBackend{failure: failure}
	session := NewSessionForTesting(screen, nil, windows, outputs, nil)
	t.Cleanup(func() { _ = session.Close() })
	dir := t.TempDir()
	for _, tc := range []struct {
		name    string
		capture func(context.Context, string) error
	}{
		{"screenshot.png", session.captureScreenshot},
		{"windows.json", session.captureWindows},
		{"active-window.json", session.captureActiveWindow},
		{"outputs.json", session.captureOutputs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			existing := []byte("existing-private-artifact")
			if err := os.WriteFile(path, existing, 0600); err != nil {
				t.Fatal(err)
			}
			if err := tc.capture(context.Background(), dir); !errors.Is(err, failure) {
				t.Fatalf("lost backend transport failure: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != string(existing) {
				t.Fatalf("failed capture modified previously published artifact: %q %v", data, err)
			}
		})
	}
	screen.failure = nil
	windows.failure = nil
	outputs.failure = nil
	for _, tc := range []struct {
		name    string
		capture func(context.Context, string) error
	}{
		{"screenshot.png", session.captureScreenshot},
		{"windows.json", session.captureWindows},
		{"active-window.json", session.captureActiveWindow},
		{"outputs.json", session.captureOutputs},
	} {
		t.Run(tc.name+" recovery", func(t *testing.T) {
			if err := tc.capture(context.Background(), dir); err != nil {
				t.Fatalf("provider recovery failed: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, tc.name))
			if err != nil || string(data) == "existing-private-artifact" {
				t.Fatalf("healthy provider did not publish new artifact: len=%d err=%v", len(data), err)
			}
		})
	}
}
