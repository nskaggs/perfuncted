package pftest

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/output"
)

type completeDiagnosticOutputs struct{}

func (completeDiagnosticOutputs) List(ctx context.Context) ([]output.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []output.Info{{Name: "fixture", Geometry: output.Geometry{W: 2, H: 2}, Available: true}}, nil
}
func (completeDiagnosticOutputs) Close() error { return nil }

// An entirely successful collection should report success, not a partial
// failure, and leave every expected diagnostic artifact on disk.
func TestCaptureOnFailureReportsCompleteBundle(t *testing.T) {
	session := NewWithOutputs(
		&Screenshotter{Frames: []image.Image{SolidImage(2, 2, color.RGBA{R: 20, A: 255})}},
		&Inputter{}, &Manager{Titles: []string{"fixture-window"}},
		completeDiagnosticOutputs{}, &Clipboard{},
	)
	defer session.Close()
	directory := filepath.Join(t.TempDir(), "complete")
	recorder := &captureFailureRecorder{failed: true}
	CaptureOnFailure(recorder, session, directory)
	if len(recorder.cleanup) != 1 {
		t.Fatalf("cleanup registrations = %d", len(recorder.cleanup))
	}
	recorder.cleanup[0]()
	for _, name := range []string{"manifest.json", "screenshot.png", "windows.json", "active-window.json", "outputs.json", "trace.txt"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if len(recorder.logs) != 1 || !strings.Contains(recorder.logs[0], "failure bundle written to "+directory) || strings.Contains(recorder.logs[0], "collection errors") {
		t.Fatalf("successful bundle reported incorrectly: %q", recorder.logs)
	}
}
