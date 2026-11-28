package perfuncted_test

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/input"
	"github.com/nskaggs/perfuncted/v2/output"
	"github.com/nskaggs/perfuncted/v2/pftest"
	"github.com/nskaggs/perfuncted/v2/window"
)

func TestCaptureFailureBundleIncludesBoundedSanitizedEvidenceWhenRequested(t *testing.T) { //nolint:gocyclo // one end-to-end fixture verifies every optional artifact and privacy bound.
	const secret = "sensitive-fixture-value"
	inputBackend := &pftest.Inputter{}
	session := pftest.NewWithOutputs(
		&pftest.Screenshotter{Frames: []image.Image{pftest.SolidImage(2, 2, color.RGBA{A: 255})}},
		inputBackend,
		&pftest.Manager{Lists: [][]window.Info{{{NativeID: "window-1", Title: "Editor", PID: 42}}}, Titles: []string{"Editor"}},
		&testOutputLister{infos: []output.Info{{Name: "DP-1", Available: true}}},
		nil,
	)
	defer session.Close()

	for range 40 {
		if err := session.Input().WithContext(context.Background()).SendKeys(secret); err != nil {
			t.Fatalf("record input receipt: %v", err)
		}
	}
	inputBackend.Err = errors.New("backend failed after dispatch")
	if err := session.Input().WithContext(context.Background()).SendKeys(secret); err == nil {
		t.Fatal("failed input operation returned nil")
	}
	inputBackend.Err = input.ErrNotSupported
	if err := session.Input().WithContext(context.Background()).SendKeys(secret); !errors.Is(err, input.ErrNotSupported) {
		t.Fatalf("unsupported input error = %v, want input.ErrNotSupported", err)
	}
	inputBackend.Err = nil

	longRole := strings.Repeat("r", 2*128)
	nodeID := accessibility.NodeID{BusName: "org.test.Editor", ObjectPath: "/window/node", Generation: 9, Incarnation: 7}
	nodes := make([]accessibility.Node, 70)
	for i := range nodes {
		nodes[i] = accessibility.Node{
			ID: nodeID, Parent: nodeID, Name: secret, Description: secret, Role: longRole,
			Interfaces: []string{"org.a11y.atspi.Accessible"}, States: []string{"enabled"},
			Text: secret, Visible: true, Showing: true, Enabled: true,
			Warnings: []string{secret},
		}
	}
	event := accessibility.Event{
		Kind: "object:property-change", Node: nodeID, Property: "accessible-name",
		Value: secret, Timestamp: time.Date(2026, time.September, 29, 12, 0, 0, 0, time.FixedZone("test", -4*60*60)),
	}
	events := make([]accessibility.Event, 70)
	for i := range events {
		events[i] = event
	}
	snapshot := &accessibility.Snapshot{
		Root: nodes[0], Nodes: nodes, Generation: 9, CapturedAt: event.Timestamp,
		Source: strings.Repeat("s", 2*128), Truncated: true,
		TruncationReasons: []string{secret}, ProviderErrors: 1, Warnings: []string{secret},
	}
	selector := accessibility.Selector{
		Role: "button", Name: secret, Label: secret, Text: secret,
		States: []string{"enabled"}, Attributes: map[string]string{"value": secret},
		Ancestors: []accessibility.AncestorSelector{{Name: secret}},
	}
	locator := session.Accessibility().LocatorForWindow("private-window-id", selector, accessibility.SnapshotOptions{
		MaxDepth: 12, MaxNodes: 70, MaxTextBytes: 4096, MaxTotalBytes: 1 << 20,
	})
	action := &accessibility.ActionReceipt{
		Node: nodes[0], Action: accessibility.Action{Index: 2, Name: secret},
		Operation: "set-text-contents", Mechanism: "at-spi.text", Dispatch: accessibility.DispatchAccepted,
		Outcome: accessibility.ActionOutcomeProof{Status: accessibility.ActionOutcomeVerified, Condition: secret, ObservedAt: event.Timestamp},
	}
	postcondition := &perfuncted.WaitEvidence{
		Evaluations: 3, Wakeups: 1, AccessibilityWakeups: 1, LastWakeSource: "accessibility-event",
		LastAccessibilityEvent: &event,
	}

	directory := filepath.Join(t.TempDir(), "bundle")
	path, err := session.WithContext(context.Background()).CaptureFailureBundle(perfuncted.FailureBundleOptions{
		Directory: directory,
		Accessibility: &perfuncted.AccessibilityFailureEvidence{
			Locator: locator, Snapshot: snapshot, Events: events, Action: action, Postcondition: postcondition,
		},
		IncludeInputReceipts: true,
	})
	if err != nil {
		t.Fatalf("CaptureFailureBundle: %v", err)
	}
	if path != directory {
		t.Fatalf("bundle path = %q, want %q", path, directory)
	}

	accessibilityPath := filepath.Join(directory, "accessibility.json")
	accessibilityData, err := os.ReadFile(accessibilityPath)
	if err != nil {
		t.Fatalf("read accessibility.json: %v", err)
	}
	if strings.Contains(string(accessibilityData), secret) {
		t.Fatalf("accessibility.json contains caller or provider content %q", secret)
	}
	var artifact map[string]any
	if err = json.Unmarshal(accessibilityData, &artifact); err != nil {
		t.Fatalf("decode accessibility.json: %v", err)
	}
	locatorSummary := requireDiagnosticMap(t, artifact["locator"])
	if locatorSummary["scope"] != "window" || locatorSummary["has_name_selector"] != true || locatorSummary["has_text_selector"] != true {
		t.Fatalf("locator summary = %v", locatorSummary)
	}
	snapshotSummary := requireDiagnosticMap(t, artifact["snapshot"])
	if snapshotSummary["node_count"] != float64(70) || snapshotSummary["omitted_nodes"] != float64(6) || snapshotSummary["warning_count"] != float64(1) {
		t.Fatalf("snapshot summary = %v", snapshotSummary)
	}
	nodeSummaries := requireDiagnosticArray(t, snapshotSummary["nodes"])
	if len(nodeSummaries) != 64 {
		t.Fatalf("summarized nodes = %d, want 64", len(nodeSummaries))
	}
	nodeSummary := requireDiagnosticMap(t, nodeSummaries[0])
	if id := requireDiagnosticMap(t, nodeSummary["id"]); id["incarnation"] != float64(nodeID.Incarnation) {
		t.Fatalf("diagnostic node ID = %v, want incarnation %d", id, nodeID.Incarnation)
	}
	if nodeSummary["has_name"] != true || nodeSummary["has_description"] != true || nodeSummary["has_text"] != true {
		t.Fatalf("node summary omitted content-presence flags: %v", nodeSummary)
	}
	if got := requireDiagnosticString(t, nodeSummary["role"]); len(got) > 128 {
		t.Fatalf("node role length = %d bytes, want at most 128", len(got))
	}
	if got := len(requireDiagnosticArray(t, artifact["events"])); got != 64 || artifact["events_omitted"] != float64(6) {
		t.Fatalf("event summary count=%d omitted=%v, want 64 and 6", got, artifact["events_omitted"])
	}
	if actionSummary := requireDiagnosticMap(t, artifact["action"]); actionSummary["has_condition"] != true || actionSummary["outcome"] != string(accessibility.ActionOutcomeVerified) {
		t.Fatalf("action summary = %v", actionSummary)
	}
	if waitSummary := requireDiagnosticMap(t, artifact["postcondition"]); waitSummary["evaluations"] != float64(3) {
		t.Fatalf("postcondition summary = %v", waitSummary)
	}

	inputPath := filepath.Join(directory, "input-receipts.json")
	inputData, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatalf("read input-receipts.json: %v", err)
	}
	if strings.Contains(string(inputData), secret) {
		t.Fatalf("input-receipts.json contains typed content %q", secret)
	}
	var receipts []input.ActionReceipt
	if err := json.Unmarshal(inputData, &receipts); err != nil {
		t.Fatalf("decode input-receipts.json: %v", err)
	}
	if len(receipts) != 32 || receipts[0].Sequence != 11 || receipts[len(receipts)-1].Sequence != 42 {
		t.Fatalf("receipt range = len:%d first:%+v last:%+v, want bounded sequences 11..42", len(receipts), receipts[0], receipts[len(receipts)-1])
	}
	uncertain := receipts[len(receipts)-2]
	if uncertain.Operation != "type" || uncertain.Dispatch != "unknown" || uncertain.FailureClass != "operation-failed" {
		t.Fatalf("uncertain input receipt = %+v", uncertain)
	}
	notSent := receipts[len(receipts)-1]
	if notSent.Dispatch != "not-sent" || notSent.FailureClass != "rejected-before-dispatch" {
		t.Fatalf("rejected input receipt = %+v", notSent)
	}
}

func requireDiagnosticMap(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("diagnostic value has type %T, want object", value)
	}
	return result
}

func requireDiagnosticArray(t *testing.T, value any) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("diagnostic value has type %T, want array", value)
	}
	return result
}

func requireDiagnosticString(t *testing.T, value any) string {
	t.Helper()
	result, ok := value.(string)
	if !ok {
		t.Fatalf("diagnostic value has type %T, want string", value)
	}
	return result
}

func TestCaptureFailureBundleOmitsSemanticArtifactsUnlessRequested(t *testing.T) {
	session := pftest.NewWithOutputs(
		&pftest.Screenshotter{Frames: []image.Image{pftest.SolidImage(1, 1, color.RGBA{A: 255})}},
		&pftest.Inputter{}, &pftest.Manager{}, &testOutputLister{}, nil,
	)
	defer session.Close()
	directory := filepath.Join(t.TempDir(), "bundle")
	if _, err := session.WithContext(context.Background()).CaptureFailureBundle(perfuncted.FailureBundleOptions{Directory: directory}); err != nil {
		t.Fatalf("CaptureFailureBundle: %v", err)
	}
	for _, name := range []string{"accessibility.json", "input-receipts.json"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("optional artifact %s stat error = %v, want not-exist", name, err)
		}
	}
}

func TestCaptureFailureBundleWritesIndependentArtifacts(t *testing.T) {
	const privateContent = "private-document-fragment-7fce43"
	screenshotter := &pftest.Screenshotter{
		Frames: []image.Image{pftest.SolidImage(4, 3, color.RGBA{R: 10, G: 20, B: 30, A: 255})},
	}
	manager := &pftest.Manager{
		Lists:  [][]window.Info{{{NativeID: "window-1", Title: "Editor", PID: 42, Active: true}}},
		Titles: []string{"Editor"},
	}
	outputs := &testOutputLister{infos: []output.Info{{
		Name:      "DP-1",
		Backend:   "wayland",
		Geometry:  output.Geometry{X: -1920, Y: 0, W: 1920, H: 1080},
		Scale:     1,
		Available: true,
	}}}
	session := pftest.NewWithOutputs(screenshotter, &pftest.Inputter{}, manager, outputs, nil)
	defer session.Close()
	if err := session.Input().WithContext(context.Background()).SendKeys("{ctrl+s}"); err != nil {
		t.Fatalf("trace setup: %v", err)
	}

	directory := filepath.Join(t.TempDir(), "bundle")
	path, err := session.WithContext(context.Background()).CaptureFailureBundle(perfuncted.FailureBundleOptions{
		Directory: directory,
		Operation: "save document",
		Error:     errors.New("document did not appear: " + privateContent),
		Metadata:  map[string]string{"case": "multi-output"},
	})
	if err != nil {
		t.Fatalf("CaptureFailureBundle: %v", err)
	}
	if path != directory {
		t.Fatalf("bundle path = %q, want %q", path, directory)
	}

	manifest := readJSONMap(t, filepath.Join(directory, "manifest.json"))
	if manifest["operation"] != "save document" {
		t.Fatalf("manifest context = %v", manifest)
	}
	assertManifestErrorRedacted(t, directory, manifest, privateContent)
	assertManifestRuntimeEvidence(t, manifest)
	artifacts, ok := manifest["artifacts"].([]any)
	if !ok || len(artifacts) != 5 {
		t.Fatalf("manifest artifacts = %v, want five artifacts", manifest["artifacts"])
	}
	trace, traceErr := os.ReadFile(filepath.Join(directory, "trace.txt"))
	if traceErr != nil || !strings.Contains(string(trace), "input type") {
		t.Fatalf("trace artifact = %q, %v", trace, traceErr)
	}
	imageFile, err := os.Open(filepath.Join(directory, "screenshot.png"))
	if err != nil {
		t.Fatal(err)
	}
	if _, decodeErr := png.Decode(imageFile); decodeErr != nil {
		t.Fatalf("screenshot.png is not a PNG: %v", decodeErr)
	}
	_ = imageFile.Close()

	var outputInfos []output.Info
	outputData, err := os.ReadFile(filepath.Join(directory, "outputs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(outputData, &outputInfos); err != nil || len(outputInfos) != 1 {
		t.Fatalf("outputs.json = %s, err=%v", outputData, err)
	}
}

func assertManifestErrorRedacted(t *testing.T, directory string, manifest map[string]any, privateContent string) {
	t.Helper()
	if manifest["format_version"] != float64(2) {
		t.Fatalf("manifest format version = %v, want 2 for the presence-only error field", manifest["format_version"])
	}
	if manifest["error_present"] != true {
		t.Fatalf("manifest error presence = %v, want true", manifest["error_present"])
	}
	if _, ok := manifest["error"]; ok {
		t.Fatalf("manifest includes raw error text: %v", manifest["error"])
	}
	manifestBytes, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifestBytes), privateContent) {
		t.Fatalf("manifest leaked error content %q", privateContent)
	}
}

func assertManifestRuntimeEvidence(t *testing.T, manifest map[string]any) {
	t.Helper()
	timeouts, ok := manifest["timeouts"].(map[string]any)
	if !ok || timeouts["medium"] == nil {
		t.Fatalf("manifest timeouts = %v, want effective timeout policy", manifest["timeouts"])
	}
	provenance, ok := manifest["timeout_provenance"].(map[string]any)
	if !ok || provenance["medium"] != "library_default" || provenance["short"] != "library_default" {
		t.Fatalf("manifest timeout provenance = %v, want library defaults", manifest["timeout_provenance"])
	}
	build, ok := manifest["build"].(map[string]any)
	if !ok || build["workspace_mode"] == nil {
		t.Fatalf("manifest build = %v, want workspace mode", manifest["build"])
	}
}

func TestCaptureFailureBundleKeepsSuccessfulArtifactsWhenCapabilityFails(t *testing.T) {
	session := pftest.New(
		&pftest.Screenshotter{Frames: []image.Image{pftest.SolidImage(1, 1, color.RGBA{A: 255})}},
		nil,
		nil,
		nil,
	)
	defer session.Close()

	directory := filepath.Join(t.TempDir(), "bundle")
	path, err := session.WithContext(context.Background()).CaptureFailureBundle(perfuncted.FailureBundleOptions{Directory: directory})
	if err == nil {
		t.Fatal("CaptureFailureBundle error = nil, want unavailable-artifact error")
	}
	if path != directory {
		t.Fatalf("bundle path = %q, want %q", path, directory)
	}
	if _, statErr := os.Stat(filepath.Join(directory, "screenshot.png")); statErr != nil {
		t.Fatalf("successful screenshot artifact missing: %v", statErr)
	}
	manifest := readJSONMap(t, filepath.Join(directory, "manifest.json"))
	errorsMap, ok := manifest["artifact_errors"].(map[string]any)
	if !ok || errorsMap["windows.json"] == nil || errorsMap["outputs.json"] == nil {
		t.Fatalf("manifest artifact errors = %v", manifest["artifact_errors"])
	}
}

type testOutputLister struct {
	infos []output.Info
}

func (l *testOutputLister) List(context.Context) ([]output.Info, error) {
	return l.infos, nil
}

func (l *testOutputLister) Close() error { return nil }

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}
