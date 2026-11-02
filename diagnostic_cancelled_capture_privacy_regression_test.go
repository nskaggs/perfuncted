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

type cancelingDiagnosticScreen struct {
	*captureTestScreenshotter
	cancel context.CancelFunc
}

func (s *cancelingDiagnosticScreen) Grab(ctx context.Context, rect image.Rectangle) (image.Image, error) {
	frame, err := s.captureTestScreenshotter.Grab(ctx, rect)
	s.cancel()
	return frame, err
}

func TestFailureBundlePreCanceledCaptureNeverCreatesDiagnosticArtifacts(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	directory := filepath.Join(t.TempDir(), "cancelled-private-diagnostics")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path, err := session.CaptureFailureBundle(ctx, FailureBundleOptions{Directory: directory, Operation: "private failure"})
	if !errors.Is(err, context.Canceled) || path != "" {
		t.Fatalf("pre-canceled bundle returned path=%q err=%v", path, err)
	}
	if _, statErr := os.Stat(directory); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("pre-canceled bundle created diagnostic directory: %v", statErr)
	}
}

func TestDiagnosticScreenshotDoesNotWriteLateCanceledSensitivePixels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &cancelingDiagnosticScreen{captureTestScreenshotter: &captureTestScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 4, 4))}, cancel: cancel}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	defer session.Close()
	directory := t.TempDir()
	err := session.captureScreenshot(ctx, directory)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late-canceled screenshot returned err=%v, want cancellation", err)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "screenshot.png")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("late-canceled screenshot disclosed pixels on disk: %v", statErr)
	}
	if backend.grabCalls != 1 {
		t.Fatalf("fixture expected completed grab, got %d", backend.grabCalls)
	}
}

type cancelingDiagnosticWindowManager struct {
	window.Manager
	cancel      context.CancelFunc
	listCalls   int
	activeCalls int
}

func (m *cancelingDiagnosticWindowManager) List(context.Context) ([]window.Info, error) {
	m.listCalls++
	m.cancel()
	return []window.Info{{ID: 17, Title: "secret-window-title"}}, nil
}
func (m *cancelingDiagnosticWindowManager) ActiveTitle(context.Context) (string, error) {
	m.activeCalls++
	m.cancel()
	return "secret-active-window", nil
}
func (*cancelingDiagnosticWindowManager) Close() error { return nil }

type cancelingDiagnosticOutputLister struct {
	cancel context.CancelFunc
	calls  int
}

func (l *cancelingDiagnosticOutputLister) List(context.Context) ([]output.Info, error) {
	l.calls++
	l.cancel()
	return []output.Info{{Name: "private-monitor"}}, nil
}
func (*cancelingDiagnosticOutputLister) Close() error { return nil }

func TestDiagnosticWindowAndOutputArtifactsRejectLateCancelledBackendData(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		fixture    func(context.CancelFunc) *Session
		capture    func(*Session, context.Context, string) error
	}{
		{"windows", "windows.json", func(cancel context.CancelFunc) *Session {
			return NewSessionForTesting(nil, nil, &cancelingDiagnosticWindowManager{cancel: cancel}, nil, nil)
		}, (*Session).captureWindows},
		{"active title", "active-window.json", func(cancel context.CancelFunc) *Session {
			return NewSessionForTesting(nil, nil, &cancelingDiagnosticWindowManager{cancel: cancel}, nil, nil)
		}, (*Session).captureActiveWindow},
		{"outputs", "outputs.json", func(cancel context.CancelFunc) *Session {
			return NewSessionForTesting(nil, nil, nil, &cancelingDiagnosticOutputLister{cancel: cancel}, nil)
		}, (*Session).captureOutputs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := tc.fixture(cancel)
			defer s.Close()
			dir := t.TempDir()
			err := tc.capture(s, ctx, dir)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("late-canceled diagnostic returned err=%v", err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, tc.file)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("canceled provider data written to %s: %v", tc.file, statErr)
			}
		})
	}
}

func TestDiagnosticArtifactCaptureDoesNotCallProviderAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	screenshot := &captureTestScreenshotter{img: image.NewRGBA(image.Rect(0, 0, 1, 1))}
	windowManager := &cancelingDiagnosticWindowManager{cancel: func() {}}
	outputs := &cancelingDiagnosticOutputLister{cancel: func() {}}
	session := NewSessionForTesting(screenshot, nil, windowManager, outputs, nil)
	defer session.Close()
	directory := t.TempDir()
	for _, tc := range []struct {
		name    string
		capture func(*Session, context.Context, string) error
	}{
		{"screenshot", (*Session).captureScreenshot},
		{"window list", (*Session).captureWindows},
		{"active title", (*Session).captureActiveWindow},
		{"outputs", (*Session).captureOutputs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.capture(session, ctx, directory); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled capture returned %v", err)
			}
		})
	}
	if screenshot.grabCalls != 0 || windowManager.listCalls != 0 || windowManager.activeCalls != 0 || outputs.calls != 0 {
		t.Fatalf("canceled diagnostics accessed providers: screen=%d list=%d title=%d outputs=%d", screenshot.grabCalls, windowManager.listCalls, windowManager.activeCalls, outputs.calls)
	}
}
