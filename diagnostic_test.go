package perfuncted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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

func assertNoDiagnosticTemporaryArtifacts(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".perfuncted-atomic-") || strings.HasPrefix(entry.Name(), ".perfuncted-png-") {
			t.Fatalf("temporary diagnostic artifact remained after operation: %s", entry.Name())
		}
	}
}

func TestDiagnosticAtomicReplacementPreservesPermissionsAndCompleteBytes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(target, []byte("old private manifest"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("{\"revision\":\"new\"}\n"), bytes.Repeat([]byte("x"), 256*1024)...)
	if err := writeAtomic(target, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("replacement data mismatch: got %d bytes, want %d", len(got), len(payload))
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private diagnostic mode = %o, want 600", info.Mode().Perm())
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}

func TestDiagnosticAtomicRenameFailurePreservesTargetAndReapsStaging(t *testing.T) {
	dir := t.TempDir()
	// An existing directory cannot be replaced by a staged regular file.
	target := filepath.Join(dir, "existing-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "important")
	if err := os.WriteFile(marker, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(target, []byte("replacement"), 0o600); err == nil {
		t.Fatal("writeAtomic replaced an existing directory")
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("existing target mutated: %q %v", got, err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}

func TestDiagnosticPNGFailedDestinationPreservesOriginalAndCleansStaging(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing-directory")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "important")
	if err := os.WriteFile(marker, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePNG(target, image.NewRGBA(image.Rect(0, 0, 2, 2))); err == nil {
		t.Fatal("PNG replaced an existing directory")
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "untouched" {
		t.Fatalf("existing target mutated: %q %v", got, err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
	if err := writePNG(filepath.Join(dir, "screen.png"), image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(dir, "screen.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := png.Decode(file); err != nil {
		t.Fatalf("written PNG invalid: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "screen.png"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("PNG file permissions %o, want 600", info.Mode().Perm())
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}

func TestDiagnosticAtomicMissingDirectoryNeverCreatesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing", "manifest.json")
	if err := writeAtomic(target, []byte("secret"), 0o600); !os.IsNotExist(err) {
		t.Fatalf("writeAtomic to missing dir: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected partial target: %v", err)
	}
	if err := writePNG(filepath.Join(dir, "missing", "screen.png"), image.NewRGBA(image.Rect(0, 0, 1, 1))); !os.IsNotExist(err) {
		t.Fatalf("writePNG to missing dir: %v", err)
	}
	assertNoDiagnosticTemporaryArtifacts(t, dir)
}

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

// Application-scoped failure evidence should identify the scope class without
// disclosing the application filter or sensitive selector strings to a
// diagnostic artifact. Scope labels must not silently fall back to "unknown".
func TestAccessibilityFailureDiagnosticClassifiesApplicationScopeWithoutIdentity(t *testing.T) {
	locator, _ := newLocatorWaitFixture(t, 1, false)
	locator.scope.application = accessibility.ApplicationFilter{Name: "secret-application-identity"}
	locator.selector = accessibility.Selector{
		Role: "button", Name: "secret-target-name", Text: "secret-text-value",
		Attributes: map[string]string{"private": "secret-attribute-value"},
	}
	artifact := summarizeAccessibilityFailure(&AccessibilityFailureEvidence{Locator: locator})
	if artifact.Locator == nil || artifact.Locator.Scope != "application" {
		t.Fatalf("application scope not classified: %+v", artifact.Locator)
	}
	if !artifact.Locator.HasName || !artifact.Locator.HasText || artifact.Locator.AttributeCount != 1 {
		t.Fatalf("application selector evidence not summarized: %+v", artifact.Locator)
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"secret-application-identity", "secret-target-name", "secret-text-value", "secret-attribute-value"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("application-scoped diagnostic leaked %q", private)
		}
	}
}

// Diagnostic metadata is bounded before it is exposed to callers. Empty
// collections stay absent rather than becoming an allocated empty list.
func TestDiagnosticLabelsBoundedUnicodeAndEmptyContract(t *testing.T) {
	if labels := diagnosticLabels(nil); labels != nil {
		t.Fatalf("nil diagnostic labels must remain nil: %#v", labels)
	}
	if labels := diagnosticLabels([]string{}); labels != nil {
		t.Fatalf("empty diagnostic labels must remain nil: %#v", labels)
	}

	long := strings.Repeat("界", maxDiagnosticLabelBytes)
	values := make([]string, maxDiagnosticListItems+5)
	for i := range values {
		values[i] = long
	}
	labels := diagnosticLabels(values)
	if len(labels) != maxDiagnosticListItems {
		t.Fatalf("diagnostic list size = %d, want %d", len(labels), maxDiagnosticListItems)
	}
	for i, label := range labels {
		if len(label) > maxDiagnosticLabelBytes || !utf8.ValidString(label) || label != strings.Repeat("界", maxDiagnosticLabelBytes/len("界")) {
			t.Fatalf("label %d improperly truncated: %q (%d bytes)", i, label, len(label))
		}
	}
}

// Missing accessibility evidence must never manufacture diagnostic records
// or emit a JSON null in place of the expected empty structured artifact.
func TestAccessibilityFailureDiagnosticMissingEvidenceIsEmptyObject(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence *AccessibilityFailureEvidence
	}{
		{name: "nil evidence"},
		{name: "empty evidence", evidence: &AccessibilityFailureEvidence{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifact := summarizeAccessibilityFailure(tc.evidence)
			if artifact.Locator != nil || artifact.Snapshot != nil || artifact.Action != nil || artifact.Postcondition != nil || len(artifact.Events) != 0 || artifact.EventsOmitted != 0 {
				t.Fatalf("missing evidence produced diagnostic contents: %+v", artifact)
			}
			payload, err := json.Marshal(artifact)
			if err != nil {
				t.Fatal(err)
			}
			if string(payload) != "{}" {
				t.Fatalf("missing evidence emitted unexpected JSON: %s", payload)
			}
		})
	}
}

// Individual capture providers can return private screenshot paths, window
// titles, and other user-controlled text. Preserve the error for the caller,
// but persist only the artifact name and generic failure status in manifests.
func TestDiagnosticArtifactErrorDoesNotPersistProviderPrivateText(t *testing.T) {
	const secret = "private-window-title-and-auth-token-c47ad"
	cause := errors.New("screenshot capture denied for " + secret)
	manifest := &failureManifest{ArtifactErrors: make(map[string]string)}
	collector := &failureArtifactCollector{manifest: manifest}
	collector.capture("screenshot.png", func() error { return cause })
	if len(collector.errors) != 1 || !errors.Is(collector.errors[0], cause) {
		t.Fatalf("original capture failure lost: %v", collector.errors)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "screenshot capture denied") {
		t.Fatalf("persisted diagnostic manifest leaked provider failure: %s", data)
	}
	if manifest.ArtifactErrors["screenshot.png"] == "" || len(manifest.Artifacts) != 0 {
		t.Fatalf("capture failure status lost: %+v", manifest)
	}
}

// Diagnostic artifacts can contain private application titles and screenshots.
// Atomic replacement must not follow an existing symlink out of the bundle.
func TestDiagnosticWriteAtomicReplacesSymlinkWithoutTouchingForeignFile(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(t.TempDir(), "unrelated-private-document")
	if err := os.WriteFile(foreign, []byte("do not modify"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.Symlink(foreign, path); err != nil {
		t.Fatal(err)
	}
	if err := writeAtomic(path, []byte("private diagnostic"), 0600); err != nil {
		t.Fatalf("atomic manifest replacement: %v", err)
	}
	got, err := os.ReadFile(foreign)
	if err != nil || string(got) != "do not modify" {
		t.Fatalf("foreign file changed: %q %v", got, err)
	}
	got, err = os.ReadFile(path)
	if err != nil || string(got) != "private diagnostic" {
		t.Fatalf("manifest bytes wrong: %q %v", got, err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatalf("manifest is not private regular file: %v %v", info, err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".perfuncted-atomic-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("stale diagnostic staging files: %v %v", matches, err)
	}
}

func TestDiagnosticWriteAtomicFailsClosedWithoutWritableParent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent", "manifest.json")
	if err := writeAtomic(missing, []byte("private"), 0600); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing parent returned %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("manifest created after invalid parent: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed write left files behind: %v %v", entries, err)
	}
}

func TestDiagnosticWritePNGRejectsMissingParentAndDoesNotWriteOutsideBundle(t *testing.T) {
	dir := t.TempDir()
	err := writePNG(filepath.Join(dir, "missing", "screen.png"), nil)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing screenshot directory returned %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("failed screenshot created files: %v %v", entries, readErr)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatalf("unexpected diagnostic data in error: %v", err)
	}
}

// Bundle creation can happen after a failed UI interaction and must never
// write diagnostic material outside the requested directory.
func TestFailureBundleRejectsUnwritableExplicitDestinationBeforeCollecting(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	root := t.TempDir()
	blocker := filepath.Join(root, "file-not-directory")
	if err := os.WriteFile(blocker, []byte("keep unrelated content"), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(blocker, "private-diagnostics")
	path, err := session.CaptureFailureBundle(context.Background(), FailureBundleOptions{Directory: destination, Operation: "private operation"})
	if err == nil || path != "" || !strings.Contains(err.Error(), "create failure bundle directory") {
		t.Fatalf("invalid directory accepted: path=%q error=%v", path, err)
	}
	data, readErr := os.ReadFile(blocker)
	if readErr != nil || string(data) != "keep unrelated content" {
		t.Fatalf("outside file modified: data=%q error=%v", data, readErr)
	}

}

func TestFailureBundleRejectsUnavailableAutomaticTempDirectory(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	inaccessible := filepath.Join(t.TempDir(), "missing-temp-directory")
	t.Setenv("TMPDIR", inaccessible)
	path, err := session.CaptureFailureBundle(context.Background(), FailureBundleOptions{Operation: "private operation"})
	if path != "" || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "create failure bundle directory") {
		t.Fatalf("missing TMPDIR accepted: path=%q error=%v", path, err)
	}
	if _, statErr := os.Stat(inaccessible); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failure created temp storage: %v", statErr)
	}
}

// Capability failures can include provider-returned titles, paths or tokens.
// The opt-in diagnostic manifest must retain the fact of failure without
// copying arbitrary provider error text into a long-lived artifact.
func TestDiagnosticCapabilityFailureDoesNotPublishProviderErrorPayload(t *testing.T) {
	const secret = "synthetic-private-document-access-token-7ec91"
	statuses := []CapabilityStatus{{
		Capability: CapabilityAccessibility,
		Requested:  true,
		Required:   true,
		Available:  false,
		Failure:    errors.New("provider rejected private payload: " + secret),
	}}
	entries := diagnosticCapabilities(statuses)
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "provider rejected private payload") {
		t.Fatalf("capability manifest leaked provider error: %s", data)
	}
	if len(entries) != 1 || entries[0].Failure == "" || entries[0].Available || !entries[0].Required {
		t.Fatalf("capability failure classification lost: %+v", entries)
	}
}

// A failure bundle aggregates failures from independent capture components.
// A repeated failure category must not multiply its entry or make artifact
// ordering depend on the order in which capture workers reported errors.
func TestDiagnosticFailureCategoriesDeduplicateAndSortDeterministically(t *testing.T) {
	input := []string{"screen unavailable", "window denied", "screen unavailable", "accessibility timed out", "window denied", "", ""}
	copyInput := append([]string(nil), input...)
	want := []string{"", "accessibility timed out", "screen unavailable", "window denied"}
	for i := 0; i < 2; i++ {
		got := uniqueSortedStrings(input)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("duplicate diagnostic categories retained or unsorted: got=%q want=%q", got, want)
		}
	}
	if !reflect.DeepEqual(input, copyInput) {
		t.Fatalf("diagnostic normalization mutated caller failures: %q", input)
	}
}

// No input operations is an authoritative empty history. Optional failure
// diagnostics must serialize it as [] rather than null, without inventing
// actions, and must not include the file when receipts were not requested.
func TestFailureDiagnosticSerializesEmptyInputReceiptsAsArray(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	dir := t.TempDir()
	manifest := &failureManifest{ArtifactErrors: make(map[string]string)}
	_ = session.captureFailureArtifacts(context.Background(), dir, manifest, FailureBundleOptions{IncludeInputReceipts: true})
	path := filepath.Join(dir, "input-receipts.json")
	data, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("empty input receipts artifact = %q, err=%v; want []", data, err)
	}
	found := false
	for _, artifact := range manifest.Artifacts {
		if artifact == "input-receipts.json" {
			found = true
		}
	}
	if !found {
		t.Fatal("empty input history missing from recorded artifacts")
	}
	second := t.TempDir()
	manifest = &failureManifest{ArtifactErrors: make(map[string]string)}
	_ = session.captureFailureArtifacts(context.Background(), second, manifest, FailureBundleOptions{})
	if _, err := os.Stat(filepath.Join(second, "input-receipts.json")); !os.IsNotExist(err) {
		t.Fatalf("unrequested input history written: %v", err)
	}
}

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

// Provider-originated action outcomes must be reduced to a fixed public
// vocabulary; otherwise arbitrary strings can leak into diagnostic artifacts.
func TestDiagnosticActionOutcomesRejectUnknownProviderValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  accessibility.DispatchOutcome
		want string
	}{
		{"accepted", accessibility.DispatchAccepted, "accepted"},
		{"rejected", accessibility.DispatchRejected, "rejected"},
		{"unknown", accessibility.DispatchUnknown, "unknown"},
		{"not sent", accessibility.DispatchNotSent, "not-sent"},
		{"empty", "", "unknown"},
		{"untrusted text", accessibility.DispatchOutcome("private window title /home/account/session"), "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := diagnosticDispatch(tc.raw); got != tc.want {
				t.Fatalf("diagnosticDispatch(%q)=%q, want %q", tc.raw, got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		raw  ActionOutcomeStatus
		want string
	}{
		{ActionOutcomeNotObserved, "not-observed"},
		{ActionOutcomeVerified, "verified"},
		{ActionOutcomeNotVerified, "not-verified"},
		{"", "not-observed"},
		{"private desktop content", "not-observed"},
	} {
		if got := diagnosticOutcome(tc.raw); got != tc.want {
			t.Fatalf("diagnosticOutcome(%q)=%q, want %q", tc.raw, got, tc.want)
		}
	}
}

// Unknown capability names can originate outside the session's declared
// capabilities; never publish them as an arbitrary startup phase.
func TestStartupCapabilityPhaseUsesFixedVocabulary(t *testing.T) {
	for _, tc := range []struct {
		capability Capability
		phase      StartupPhase
	}{
		{CapabilityScreen, StartupPhaseCapabilityScreen},
		{CapabilityInput, StartupPhaseCapabilityInput},
		{CapabilityWindows, StartupPhaseCapabilityWindows},
		{CapabilityOutputs, StartupPhaseCapabilityOutputs},
		{CapabilityClipboard, StartupPhaseCapabilityClipboard},
		{CapabilityAccessibility, StartupPhaseCapabilityAccessibility},
		{Capability("private-backend-path"), ""},
		{"", ""},
	} {
		if got := capabilityStartupPhase(tc.capability); got != tc.phase {
			t.Fatalf("capabilityStartupPhase(%q)=%q, want %q", tc.capability, got, tc.phase)
		}
	}
}

// An absent clipboard facade is not an authorization to inspect the desktop.
func TestNilClipboardFacadeCannotPassAvailabilityGate(t *testing.T) {
	var clipboard *ClipboardBundle
	if err := clipboard.checkAvailable("get"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil clipboard availability: %v, want ErrNilSession", err)
	}
}

// An explicit GOWORK file may be located under private directories and
// include user or project identifiers. A persistent bundle should describe
// build mode, never copy that filesystem path into diagnostic metadata.
func TestDiagnosticBuildInfoRedactsExplicitGoWorkspacePath(t *testing.T) {
	const secret = "/home/private-user/projects/internal-product/workspace.go.work"
	for _, tc := range []struct{ value, want string }{
		{"", "default"},
		{"off", "off"},
		{secret, "on"},
	} {
		t.Run(tc.want+"-"+strings.ReplaceAll(tc.value, "/", "_"), func(t *testing.T) {
			t.Setenv("GOWORK", tc.value)
			info := currentBuildInfo()
			if info.WorkspaceMode != tc.want {
				t.Fatalf("workspace mode=%q, want %q", info.WorkspaceMode, tc.want)
			}
			if strings.Contains(info.WorkspaceMode, secret) {
				t.Fatalf("workspace path exposed in diagnostic: %q", info.WorkspaceMode)
			}
		})
	}
}
