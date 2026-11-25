package perfuncted

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

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
