package pftest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/v2/output"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A canceled read must neither disclose a title nor consume the next mock
// observation. Otherwise later fixture operations become order-dependent.
func TestActiveTitleCancellationDoesNotAdvancePlayback(t *testing.T) {
	manager := &Manager{Titles: []string{"first", "second"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := manager.ActiveTitle(ctx)
	if got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled title = %q, %v; want empty and context.Canceled", got, err)
	}
	title, err := manager.ActiveTitle(context.Background())
	if err != nil || title != "first" {
		t.Fatalf("next title = %q, %v; want first observation", title, err)
	}
}

// A recording test facade lets the cleanup run as though a test has failed
// without failing the outer Go test, and keeps real failure artifacts private.
type captureFailureRecorder struct {
	testing.TB
	failed  bool
	cleanup []func()
	logs    []string
}

func (r *captureFailureRecorder) Helper()          {}
func (r *captureFailureRecorder) Cleanup(f func()) { r.cleanup = append(r.cleanup, f) }
func (r *captureFailureRecorder) Failed() bool     { return r.failed }
func (r *captureFailureRecorder) Name() string     { return "synthetic-failure" }
func (r *captureFailureRecorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func TestCaptureOnFailureRegistersCleanupOnlyForFailure(t *testing.T) {
	session := New(&Screenshotter{Frames: []image.Image{SolidImage(2, 2, color.RGBA{R: 20, A: 255})}}, &Inputter{}, &Manager{}, &Clipboard{})
	defer session.Close()
	directory := filepath.Join(t.TempDir(), "diagnostic")
	recorder := &captureFailureRecorder{}
	CaptureOnFailure(nil, session, directory)
	CaptureOnFailure(recorder, nil, directory)
	if len(recorder.cleanup) != 0 {
		t.Fatalf("nil arguments registered cleanup: %d", len(recorder.cleanup))
	}
	CaptureOnFailure(recorder, session, directory)
	if len(recorder.cleanup) != 1 {
		t.Fatalf("cleanup callbacks = %d, want one", len(recorder.cleanup))
	}
	recorder.cleanup[0]()
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("passing test produced diagnostic artifacts: %v", err)
	}
	recorder.failed = true
	recorder.cleanup[0]()
	contents, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatalf("failed test did not produce manifest: %v", err)
	}
	var manifest struct {
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Operation != "test synthetic-failure" {
		t.Fatalf("failure bundle operation = %q", manifest.Operation)
	}
	if len(recorder.logs) != 1 || !strings.Contains(recorder.logs[0], "failure bundle written to") {
		t.Fatalf("missing failure artifact status: %q", recorder.logs)
	}
}

// Missing capabilities must be reported as a partial collection failure,
// never silently presented as a complete diagnostic bundle.
func TestCaptureOnFailureLogsPartialCollectionError(t *testing.T) {
	session := New(nil, nil, nil, nil)
	defer session.Close()
	directory := filepath.Join(t.TempDir(), "partial-bundle")
	recorder := &captureFailureRecorder{failed: true}
	CaptureOnFailure(recorder, session, directory)
	if len(recorder.cleanup) != 1 {
		t.Fatalf("missing cleanup registration: %d", len(recorder.cleanup))
	}
	recorder.cleanup[0]()
	if _, err := os.Stat(filepath.Join(directory, "manifest.json")); err != nil {
		t.Fatalf("partial bundle omitted manifest: %v", err)
	}
	if len(recorder.logs) != 1 || !strings.Contains(recorder.logs[0], "with collection errors:") || !strings.Contains(recorder.logs[0], directory) {
		t.Fatalf("partial failure hidden from test output: %q", recorder.logs)
	}
}

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
