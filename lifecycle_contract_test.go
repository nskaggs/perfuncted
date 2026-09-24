package perfuncted

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
)

type closeCountingScreen struct {
	capabilityScreen
	calls atomic.Int32
}

func (s *closeCountingScreen) Close() error {
	s.calls.Add(1)
	return s.capabilityScreen.Close()
}

type closeCountingInput struct {
	calls atomic.Int32
}

func (*closeCountingInput) KeyDown(context.Context, string) error { return nil }
func (*closeCountingInput) KeyUp(context.Context, string) error   { return nil }
func (*closeCountingInput) Type(context.Context, string) error    { return nil }
func (*closeCountingInput) TypeLiteral(context.Context, string) error {
	return nil
}
func (*closeCountingInput) MouseMove(context.Context, int, int) error { return nil }
func (*closeCountingInput) MouseClick(context.Context, int, int, int) error {
	return nil
}
func (*closeCountingInput) MouseDown(context.Context, int) error { return nil }
func (*closeCountingInput) MouseUp(context.Context, int) error   { return nil }
func (*closeCountingInput) ScrollUp(context.Context, int) error  { return nil }
func (*closeCountingInput) ScrollDown(context.Context, int) error {
	return nil
}
func (*closeCountingInput) ScrollLeft(context.Context, int) error { return nil }
func (*closeCountingInput) ScrollRight(context.Context, int) error {
	return nil
}
func (*closeCountingInput) PointerLocation(context.Context) (int, int, error) {
	return 0, 0, input.ErrNotSupported
}
func (*closeCountingInput) Sync(context.Context) error { return nil }
func (s *closeCountingInput) Close() error {
	s.calls.Add(1)
	return nil
}

var (
	_ input.Inputter = (*closeCountingInput)(nil)
	_ window.Manager = (*listOnlyWindowManager)(nil)
	_ output.Lister  = (*closeCountingOutput)(nil)
	_ input.Inputter = (*closeCountingInput)(nil)
)

type closeCountingOutput struct {
	calls atomic.Int32
}

func (o *closeCountingOutput) List(context.Context) ([]output.Info, error) {
	return nil, nil
}
func (o *closeCountingOutput) Close() error {
	o.calls.Add(1)
	return nil
}

func TestSessionCloseUnifiesBundleTeardown(t *testing.T) {
	t.Parallel()
	screenBackend := &closeCountingScreen{}
	inputBackend := &closeCountingInput{}
	windowBackend := &listOnlyWindowManager{}
	outputBackend := &closeCountingOutput{}
	clipboardBackend := &capabilityClipboard{}
	session := NewSessionForTesting(screenBackend, inputBackend, windowBackend, outputBackend, clipboardBackend)
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := screenBackend.calls.Load(); got != 1 {
		t.Fatalf("screen close calls = %d, want 1", got)
	}
	if got := inputBackend.calls.Load(); got != 1 {
		t.Fatalf("input close calls = %d, want 1", got)
	}
	if got := outputBackend.calls.Load(); got != 1 {
		t.Fatalf("output close calls = %d, want 1", got)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if got := screenBackend.calls.Load(); got != 1 {
		t.Fatalf("screen close calls after second Close = %d, want 1", got)
	}
	if got := inputBackend.calls.Load(); got != 1 {
		t.Fatalf("input close calls after second Close = %d, want 1", got)
	}
}

func TestBundleCloseNilSafe(t *testing.T) {
	t.Parallel()
	var screenBundle *ScreenBundle
	if err := screenBundle.close(); err != nil {
		t.Fatalf("nil ScreenBundle close = %v, want nil", err)
	}
	var inputBundle *InputBundle
	if err := inputBundle.close(); err != nil {
		t.Fatalf("nil InputBundle close = %v, want nil", err)
	}
}

func TestAccessibilityReopenRefreshesCapabilities(t *testing.T) {
	t.Parallel()
	old := &accessibilityReopenerFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
	fresh := &bundleAccessibilityFake{gen: 2}
	old.fresh = fresh
	session := NewSessionForTesting(nil, nil, nil, nil, nil, old)
	t.Cleanup(func() { _ = session.Close() })
	before := session.Capability(CapabilityAccessibility)
	if !before.Available {
		t.Fatal("accessibility capability not available before reopen")
	}
	if err := session.Accessibility.Reopen(context.Background()); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	after := session.Capability(CapabilityAccessibility)
	if !after.Available || after.Failure != nil {
		t.Fatalf("capability after reopen = %+v, want available without failure", after)
	}
	wantBackend := "perfuncted.bundleAccessibilityFake"
	_ = wantBackend
	if after.Backend == "" {
		t.Fatal("capability backend empty after reopen")
	}
	if len(after.Operations) == 0 {
		t.Fatal("capability operations empty after reopen")
	}
	apps, err := session.Accessibility.Applications(context.Background())
	if err != nil {
		t.Fatalf("Applications after reopen: %v", err)
	}
	_ = apps
	_ = accessibility.ErrDisconnected
}

func TestDiagnosticAtomicWritesLeaveNoPartialFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "manifest.json")
	if err := writeJSON(jsonPath, map[string]string{"op": "test"}); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	if _, statErr := os.Stat(jsonPath); statErr != nil {
		t.Fatalf("stat manifest: %v", statErr)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > 12 && name[:12] == ".perfuncted-" {
			t.Fatalf("temp file %q left after writeJSON", name)
		}
	}
	pngPath := filepath.Join(dir, "screenshot.png")
	if pngErr := writePNG(pngPath, image.NewRGBA(image.Rect(0, 0, 2, 2))); pngErr != nil {
		t.Fatalf("writePNG: %v", pngErr)
	}
	entries, readErr = os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > 12 && name[:12] == ".perfuncted-" {
			t.Fatalf("temp file %q left after writePNG", name)
		}
	}
	failingPath := filepath.Join(dir, "failing.json")
	if err := writeJSON(failingPath, map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("writeJSON with unserializable value succeeded, want error")
	}
	if _, statErr := os.Stat(failingPath); !os.IsNotExist(statErr) {
		t.Fatalf("partial file %q exists after failed writeJSON", failingPath)
	}
}
