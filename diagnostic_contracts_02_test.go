package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Diagnostic screenshots bypass the public ScreenBundle, but must enforce the
// same backing-image validity before PNG encoding sensitive desktop pixels.
func TestDiagnosticScreenshotRejectsTruncatedPixelBufferWithoutOverwriting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{
		{"RGBA", &image.RGBA{Rect: image.Rect(0, 0, 2, 2), Stride: 8, Pix: make([]byte, 11)}},
		{"Gray", &image.Gray{Rect: image.Rect(0, 0, 2, 2), Stride: 2, Pix: make([]byte, 3)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: tc.frame}, nil, nil, nil, nil)
			defer session.Close()
			dir := t.TempDir()
			path := filepath.Join(dir, "screenshot.png")
			if err := os.WriteFile(path, []byte("original-private-capture"), 0600); err != nil {
				t.Fatal(err)
			}
			err := session.captureScreenshot(context.Background(), dir)
			if err == nil || !strings.Contains(err.Error(), "invalid capture") {
				t.Fatalf("malformed diagnostic frame accepted: %v", err)
			}
			bytes, readErr := os.ReadFile(path)
			if readErr != nil || string(bytes) != "original-private-capture" {
				t.Fatalf("existing capture overwritten: bytes=%q err=%v", bytes, readErr)
			}
			assertNoDiagnosticTemporaryArtifacts(t, dir)
			if errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unexpected filesystem failure rather than capture validation: %v", err)
			}
		})
	}
}

// Bundle collection is best-effort, but failure to publish the manifest must
// be returned even when earlier artifacts succeeded. The atomic writer must
// not replace an existing directory or leave private staging files behind.
func TestFailureBundleManifestPublicationFailurePreservesCompletedArtifacts(t *testing.T) {
	s := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer s.Close()
	directory := t.TempDir()
	blocked := filepath.Join(directory, "manifest.json")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(blocked, "unrelated-private")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := s.CaptureFailureBundle(context.Background(), FailureBundleOptions{Directory: directory, Operation: "redacted operation"})
	if actual != directory || err == nil || !strings.Contains(err.Error(), "manifest.json") {
		t.Fatalf("manifest publication failure was lost: path=%q err=%v", actual, err)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil || string(data) != "keep" {
		t.Fatalf("manifest collision damaged existing data: %q err=%v", data, readErr)
	}
	trace, traceErr := os.Stat(filepath.Join(directory, "trace.txt"))
	if traceErr != nil || !trace.Mode().IsRegular() || trace.Mode().Perm() != 0600 {
		t.Fatalf("successful trace artifact not retained privately: %+v err=%v", trace, traceErr)
	}
	assertNoDiagnosticTemporaryArtifacts(t, directory)
}

// Invalid callers must be rejected before any filesystem work, including
// when they request an explicit directory that has not yet been created.
func TestFailureBundleNilSessionAndContextCannotCreateArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "uncreated")
	var absent *Session
	got, err := absent.CaptureFailureBundle(context.Background(), FailureBundleOptions{Directory: path})
	if got != "" || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil session returned path=%q err=%v", got, err)
	}
	s := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer s.Close()
	got, err = s.CaptureFailureBundle(nil, FailureBundleOptions{Directory: path})
	if got != "" || !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil context returned path=%q err=%v", got, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid call created artifact directory: %v", err)
	}
}

// A backend may return a nil image after a failed capture. Diagnostic
// generation must report failure rather than panicking or publishing a file.
func TestDiagnosticPNGWriterRejectsAbsentImagesWithoutPanicking(t *testing.T) {
	for _, tc := range []struct {
		name string
		img  image.Image
	}{
		{name: "nil interface"},
		{name: "typed nil RGBA", img: (*image.RGBA)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "capture.png")
			if err := writePNG(path, tc.img); err == nil {
				t.Fatal("missing screenshot accepted")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("missing capture published file: %v", err)
			}
		})
	}
}

// Non-nil screenshots can still have no encodable pixels. A failed PNG
// encoder must leave neither a published capture nor a temporary artifact.
func TestDiagnosticPNGEncodingRejectsZeroSizedImageAndCleansStaging(t *testing.T) {
	for _, rect := range []image.Rectangle{
		image.Rect(0, 0, 0, 0),
		image.Rect(0, 0, 4, 0),
		image.Rect(0, 0, 0, 4),
	} {
		t.Run(rect.String(), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "screen.png")
			err := writePNG(path, image.NewRGBA(rect))
			if err == nil || !strings.Contains(err.Error(), "invalid image size") {
				t.Fatalf("zero-pixel screenshot unexpectedly encoded: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("zero-pixel screenshot published: %v", err)
			}
			assertNoDiagnosticTemporaryArtifacts(t, dir)
		})
	}
}

// A capture backend may return a non-nil image with no usable pixels. PNG
// encoding must fail without publishing or overwriting a prior screenshot,
// and the private temporary file must be removed on that failure.
func TestDiagnosticPNGEncodeFailureLeavesExistingScreenshotAndNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "screen.png")
	original := []byte("original private capture")
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := writePNG(target, image.NewRGBA(image.Rectangle{})); err == nil {
		t.Fatal("empty image accepted as diagnostic PNG")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != string(original) {
		t.Fatalf("failed PNG write changed existing capture: %q %v", data, err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}

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

func TestTimeoutProvenanceTracksExplicitAndDefaultFields(t *testing.T) {
	configured := TimeoutPolicy{Medium: 17 * time.Second}
	session := &Session{
		config:          SessionConfig{Timeouts: configured.WithDefaults()},
		timeoutInput:    configured,
		hasTimeoutInput: true,
	}

	got := session.timeoutProvenance()
	for field, want := range map[string]string{
		"short":      "library_default",
		"medium":     "session_config",
		"long":       "library_default",
		"startup":    "library_default",
		"diagnostic": "library_default",
		"poll":       "library_default",
	} {
		if got[field] != want {
			t.Errorf("timeout provenance for %s = %q, want %q", field, got[field], want)
		}
	}
}

// Diagnostic collection is best-effort. An untrusted capture backend returning
// a typed nil image must produce an artifact error, not crash collection.
func TestDiagnosticScreenshotRejectsTypedNilWithoutPublishingArtifact(t *testing.T) {
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: (*image.RGBA)(nil)}, nil, nil, nil, nil)
	defer session.Close()
	dir := t.TempDir()
	err := session.captureScreenshot(context.Background(), dir)
	if err == nil || !strings.Contains(err.Error(), "nil image") {
		t.Fatalf("typed nil screenshot accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "screenshot.png")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid screenshot published: %v", err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}
