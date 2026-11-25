package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"slices"
	"strings"
	"testing"
)

func TestAccessibilityOutlineTextDistinguishesProviderIncompleteSnapshot(t *testing.T) {
	var output bytes.Buffer
	err := writeAccessibilityText(&output, accessibility.Outline{
		Generation: 7, ProviderErrors: 2, Warnings: []string{"child read failed"},
	})
	if err != nil {
		t.Fatalf("writeAccessibilityText: %v", err)
	}
	if !strings.Contains(output.String(), "truncated=false provider_errors=2") {
		t.Fatalf("outline diagnostic = %q, want distinct provider-incomplete status", output.String())
	}
}

type cliAccessibilityFake struct{}

type cliAutomationFake struct{ cliAccessibilityFake }

func (cliAccessibilityFake) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events"}
}
func (cliAccessibilityFake) Applications(context.Context) ([]accessibility.Application, error) {
	return []accessibility.Application{{Node: accessibility.Node{ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app"}, Name: "Test App", Role: "application"}, PID: 123}}, nil
}
func (cliAccessibilityFake) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) { //nolint:unparam // the fake models a successful resolver.
	return accessibility.Application{Node: accessibility.Node{ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}, Name: "Test App", Role: "application"}, PID: 123}, nil
}
func (cliAccessibilityFake) Snapshot(_ context.Context, rootID accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	if rootID == (accessibility.NodeID{}) {
		rootID = accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}
	}
	root := accessibility.Node{ID: rootID, Name: "Test App", Role: "application"}
	save := accessibility.Node{ID: accessibility.NodeID{BusName: rootID.BusName, ObjectPath: "/save", Generation: 1}, Name: "Save", Role: "button", Actions: []accessibility.Action{{Index: 1, Name: "press"}}}
	entry := accessibility.Node{ID: accessibility.NodeID{BusName: rootID.BusName, ObjectPath: "/entry", Generation: 1}, Name: "Name", Role: "entry", Text: "current"}
	return accessibility.Snapshot{Root: root, Nodes: []accessibility.Node{root, save, entry}, Generation: 1, Source: "fake"}, nil
}
func (cliAccessibilityFake) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	snapshot, err := (cliAccessibilityFake{}).Snapshot(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	return accessibility.FilterSnapshot(snapshot, query), nil
}
func (cliAccessibilityFake) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{Name: "focused"}, nil
}
func (cliAccessibilityFake) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{Name: "point"}, nil
}
func (cliAccessibilityFake) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) { //nolint:unparam // the fake intentionally models a successful stream.
	stream := make(chan accessibility.Event)
	close(stream)
	return stream, nil
}
func (cliAccessibilityFake) Close() error { return nil }

type cliEventsFake struct {
	cliAccessibilityFake
	err error
}

func (f cliEventsFake) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	stream := make(chan accessibility.Event, 1)
	stream <- accessibility.Event{Kind: "object:property-change", Property: "Name", Value: "updated"}
	close(stream)
	return stream, f.err
}

func (cliAutomationFake) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "invoke-action", "invoke-action-by-name", "invoke-default-action", "grab-focus", "scroll", "scroll-to-point", "set-position", "set-size", "set-extents", "set-value", "set-text-contents", "insert-text", "delete-text", "copy-text", "cut-text", "paste-text", "set-caret", "set-text-selection", "add-text-selection", "remove-text-selection", "set-document-text-selections", "select-child", "deselect-child", "select-all", "clear-selection", "deselect-selected-child", "select-row", "deselect-row", "select-column", "deselect-column", "window-root", "reopen"}
}

type cliAutomationCapabilitiesFake struct {
	cliAutomationFake
	operations []string
}

func (f cliAutomationCapabilitiesFake) SupportedOperations() []string {
	return slices.Clone(f.operations)
}

func (cliAutomationFake) InvokeAction(context.Context, accessibility.NodeID, int32) error {
	return nil
}
func (cliAutomationFake) InvokeActionByName(context.Context, accessibility.NodeID, string) (accessibility.Action, error) { //nolint:unparam // the CLI fixture models a successful named action.
	return accessibility.Action{Index: 1, Name: "named"}, nil
}
func (cliAutomationFake) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) { //nolint:unparam // the CLI fixture intentionally models a successful action.
	return accessibility.Action{Index: 0, Name: "default"}, nil
}

type cliVerifiedActionFake struct {
	cliAutomationFake
	actionCalls           int
	mutateState           bool
	changeBeforeDispatch  bool
	changeRootAfterAction bool
	snapshotCalls         int
}

func (f *cliVerifiedActionFake) Snapshot(_ context.Context, rootID accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.snapshotCalls++
	if rootID == (accessibility.NodeID{}) {
		rootID = accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1, Incarnation: 1}
	}
	rootID.Incarnation = 1
	if f.changeRootAfterAction && f.actionCalls > 0 {
		rootID.Incarnation = 2
	}
	root := accessibility.Node{ID: rootID, Name: "Test App", Role: "application"}
	save := accessibility.Node{ID: accessibility.NodeID{BusName: rootID.BusName, ObjectPath: "/save", Generation: 1, Incarnation: 1}, Name: "Save", Role: "button", Text: "Save", Actions: []accessibility.Action{{Index: 0, Name: "default"}}}
	entry := accessibility.Node{ID: accessibility.NodeID{BusName: rootID.BusName, ObjectPath: "/status", Generation: 1, Incarnation: 1}, Name: "Status", Role: "label", Text: "Ready"}
	if f.actionCalls > 0 && f.mutateState {
		save.Name = "Saved"
		entry.Text = "Saved"
	}
	if f.changeBeforeDispatch && f.actionCalls == 0 && f.snapshotCalls > 1 {
		save.Text = "Changed externally"
	}
	return accessibility.Snapshot{Root: root, Nodes: []accessibility.Node{root, save, entry}, Generation: 1}, nil
}

func (f *cliVerifiedActionFake) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	snapshot, err := f.Snapshot(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	return accessibility.FilterSnapshot(snapshot, query), nil
}

func (f *cliVerifiedActionFake) FindApplication(ctx context.Context, filter accessibility.ApplicationFilter) (accessibility.Application, error) {
	app, err := f.cliAutomationFake.FindApplication(ctx, filter)
	if err != nil {
		return accessibility.Application{}, err
	}
	app.ID.Incarnation = 1
	return app, nil
}

func (f *cliVerifiedActionFake) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) {
	f.actionCalls++
	return accessibility.Action{Index: 0, Name: "default"}, nil
}

func (cliAutomationFake) GrabFocus(context.Context, accessibility.NodeID) error { return nil }
func (cliAutomationFake) ScrollTo(context.Context, accessibility.NodeID, accessibility.ScrollType) error {
	return nil
}
func (cliAutomationFake) ScrollToPoint(context.Context, accessibility.NodeID, accessibility.CoordType, int, int) error {
	return nil
}
func (cliAutomationFake) SetPosition(context.Context, accessibility.NodeID, int, int, accessibility.CoordType) error {
	return nil
}
func (cliAutomationFake) SetSize(context.Context, accessibility.NodeID, int, int) error { return nil }
func (cliAutomationFake) SetExtents(context.Context, accessibility.NodeID, int, int, int, int, accessibility.CoordType) error {
	return nil
}
func (cliAutomationFake) SetValue(context.Context, accessibility.NodeID, float64) error { return nil }
func (cliAutomationFake) SetTextContents(context.Context, accessibility.NodeID, string) error {
	return nil
}
func (cliAutomationFake) InsertText(context.Context, accessibility.NodeID, int32, string) error {
	return nil
}
func (cliAutomationFake) DeleteText(context.Context, accessibility.NodeID, int32, int32) error {
	return nil
}
func (cliAutomationFake) CopyText(context.Context, accessibility.NodeID, int32, int32) error {
	return nil
}
func (cliAutomationFake) CutText(context.Context, accessibility.NodeID, int32, int32) error {
	return nil
}
func (cliAutomationFake) PasteText(context.Context, accessibility.NodeID, int32) error { return nil }
func (cliAutomationFake) SetCaretOffset(context.Context, accessibility.NodeID, int32) error {
	return nil
}
func (cliAutomationFake) SetTextSelection(context.Context, accessibility.NodeID, int32, int32, int32) error {
	return nil
}
func (cliAutomationFake) AddTextSelection(context.Context, accessibility.NodeID, int32, int32) error {
	return nil
}
func (cliAutomationFake) RemoveTextSelection(context.Context, accessibility.NodeID, int32) error {
	return nil
}
func (cliAutomationFake) SetTextSelections(context.Context, accessibility.NodeID, []accessibility.DocumentTextSelection) error {
	return nil
}
func (cliAutomationFake) SelectChild(context.Context, accessibility.NodeID, int32) error { return nil }
func (cliAutomationFake) DeselectChild(context.Context, accessibility.NodeID, int32) error {
	return nil
}
func (cliAutomationFake) SelectAll(context.Context, accessibility.NodeID) error      { return nil }
func (cliAutomationFake) ClearSelection(context.Context, accessibility.NodeID) error { return nil }
func (cliAutomationFake) DeselectSelectedChild(context.Context, accessibility.NodeID) error {
	return nil
}
func (cliAutomationFake) SelectRow(context.Context, accessibility.NodeID, int32) error    { return nil }
func (cliAutomationFake) DeselectRow(context.Context, accessibility.NodeID, int32) error  { return nil }
func (cliAutomationFake) SelectColumn(context.Context, accessibility.NodeID, int32) error { return nil }
func (cliAutomationFake) DeselectColumn(context.Context, accessibility.NodeID, int32) error {
	return nil
}
func (cliAutomationFake) Reopen(context.Context) (accessibility.Backend, error) {
	return cliAutomationFake{}, nil
}

func (cliAutomationFake) ResolveWindow(_ context.Context, target accessibility.WindowTarget) (accessibility.WindowScope, error) { //nolint:unparam // the fake models a successful resolver.
	appID := accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}
	return accessibility.WindowScope{
		WindowID:        target.ID,
		Root:            accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/window", Generation: 1},
		ApplicationRoot: appID,
		Application:     accessibility.Application{Node: accessibility.Node{ID: appID, Name: "Test App", Role: "application"}, PID: 123},
		PID:             123,
		Generation:      1,
	}, nil
}

type cliAmbiguousFake struct{ cliAutomationFake }

func (cliAmbiguousFake) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	snapshot, err := (cliAmbiguousFake{}).Snapshot(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	return accessibility.FilterSnapshot(snapshot, query), nil
}

func (cliAmbiguousFake) Snapshot(_ context.Context, rootID accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	if rootID == (accessibility.NodeID{}) {
		rootID = accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}
	}
	root := accessibility.Node{ID: rootID, Name: "Test App", Role: "application"}
	one := accessibility.Node{ID: accessibility.NodeID{BusName: rootID.BusName, ObjectPath: "/one", Generation: 1}, Name: "one", Role: "button"}
	two := accessibility.Node{ID: accessibility.NodeID{BusName: rootID.BusName, ObjectPath: "/two", Generation: 1}, Name: "two", Role: "button"}
	return accessibility.Snapshot{Root: root, Nodes: []accessibility.Node{root, one, two}, Generation: 1, Source: "fake"}, nil
}

func TestAccessibilityCLIApplicationsJSON(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "apps", "--json"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAccessibilityFake{}), nil
		}
	})
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var apps []accessibility.Application
	if err := json.Unmarshal([]byte(stdout), &apps); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if len(apps) != 1 || apps[0].PID != 123 || !strings.Contains(apps[0].Name, "Test") {
		t.Fatalf("apps=%+v", apps)
	}
}

func TestAccessibilityCLIHumanOutputIsTheDefault(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "apps"}, openCLIWithAccessibility)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Test App\tpid=123") || strings.HasPrefix(stdout, "{") || strings.HasPrefix(stdout, "[") {
		t.Fatalf("human apps output = %q", stdout)
	}
}

func TestAccessibilityCLIHasNoUnreleasedAlias(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "apps"}, openCLIWithAccessibility)
	if code == 0 || stdout != "" || !strings.Contains(stderr, `unknown command "accessibility"`) {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}

	stdout, stderr, code = captureRunIO(t, []string{"a11y", "action", "--action-name", "press"}, openCLIWithAccessibility)
	if code == 0 || !strings.Contains(stderr, "unknown flag: --action-name") {
		t.Fatalf("semantic action accepted an unreleased flag alias: code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}

	stdout, stderr, code = captureRunIO(t, []string{"a11y", "raw", "--help"}, openCLIWithAccessibility)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "--action-name string") {
		t.Fatalf("raw action-name flag missing: code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
}

func TestAccessibilityCLIEventsDefaultToJSONLines(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "events"}, openCLIWithEvents)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	var event accessibility.Event
	if err := json.Unmarshal([]byte(stdout), &event); err != nil {
		t.Fatalf("events output is not JSONL: %q: %v", stdout, err)
	}
	if event.Kind != "object:property-change" || event.Property != "Name" || event.Value != "updated" {
		t.Fatalf("event=%+v", event)
	}
}

func TestAccessibilityCLIEventsRejectJSONFormatFlag(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "events", "--json"}, openCLIWithEvents)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "unknown flag: --json") {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
}

func TestAccessibilityCLIWorkflowCommandsUseSemanticScopeAndHelpers(t *testing.T) {
	for _, args := range [][]string{
		{"a11y", "action", "--app", "Test", "--role", "button", "--json"},
		{"a11y", "focus", "--app", "Test", "--role", "button"},
		{"a11y", "text", "--app", "Test", "--role", "entry", "--value", "updated"},
	} {
		stdout, stderr, code := captureRunIO(t, args, openCLIWithAutomation)
		if code != 0 || stderr != "" {
			t.Fatalf("args=%v code=%d stderr=%q stdout=%q", args, code, stderr, stdout)
		}
	}
}

func TestAccessibilityCLIVerifiedActionTextAndChangedState(t *testing.T) {
	for _, expectation := range [][]string{
		{"--expect-text", "Saved"},
		{"--expect-state", "changed"},
	} {
		t.Run(strings.Join(expectation, "-"), func(t *testing.T) {
			backend := &cliVerifiedActionFake{mutateState: true}
			factory := func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
				}
			}
			args := append([]string{"a11y", "action", "--app", "Test", "--role", "button", "--name", "Save"}, expectation...)
			args = append(args, "--timeout", "1s", "--json")
			stdout, stderr, code := captureRunIO(t, args, factory)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
			}
			var result accessibilityActionCLIResult
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatalf("verified action output is not JSON: %q: %v", stdout, err)
			}
			if result.Status != "verified" || result.Receipt.Dispatch != accessibility.DispatchAccepted || result.Receipt.Outcome.Status != perfuncted.ActionOutcomeVerified {
				t.Fatalf("verified action result = %+v", result)
			}
			if result.Evidence.Evaluations == 0 || backend.actionCalls != 1 {
				t.Fatalf("evidence=%+v actionCalls=%d, want observed state after one dispatch", result.Evidence, backend.actionCalls)
			}
		})
	}
}

func TestAccessibilityCLIVerificationTimeoutPreservesDispatchAndDoesNotLeakExpectedText(t *testing.T) {
	backend := &cliVerifiedActionFake{}
	factory := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
		}
	}
	// This backend deliberately performs the action without exposing the
	// requested sentinel in its fresh state.
	stdout, stderr, code := captureRunIO(t, []string{
		"a11y", "action", "--app", "Test", "--role", "button", "--name", "Save",
		"--expect-text", "private-sentinel", "--timeout", "10ms", "--json",
	}, factory)
	if code == 0 || !strings.Contains(stderr, "deadline exceeded") {
		t.Fatalf("code=%d stderr=%q stdout=%q, want verification timeout", code, stderr, stdout)
	}
	var result accessibilityActionCLIResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("verification failure output is not JSON: %q: %v", stdout, err)
	}
	if result.Status != "verification-timeout" || result.ErrorCode != "deadline-exceeded" || result.Receipt.Dispatch != accessibility.DispatchAccepted || result.Receipt.Outcome.Status != perfuncted.ActionOutcomeNotVerified {
		t.Fatalf("timeout result = %+v", result)
	}
	if backend.actionCalls != 1 {
		t.Fatalf("action calls=%d, want exactly one after timeout", backend.actionCalls)
	}
	if strings.Contains(stdout+stderr, "private-sentinel") {
		t.Fatalf("expected text leaked in result/error: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestAccessibilityCLIChangedBaselineMustStillMatchBeforeDispatch(t *testing.T) {
	backend := &cliVerifiedActionFake{changeBeforeDispatch: true}
	factory := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
		}
	}
	stdout, stderr, code := captureRunIO(t, []string{
		"a11y", "action", "--app", "Test", "--role", "button", "--name", "Save",
		"--expect-state", "changed", "--timeout", "1s", "--json",
	}, factory)
	if code == 0 || !strings.Contains(stderr, "baseline target changed before dispatch") {
		t.Fatalf("code=%d stderr=%q stdout=%q, want pre-dispatch baseline rejection", code, stderr, stdout)
	}
	var result accessibilityActionCLIResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("preflight failure output is not JSON: %q: %v", stdout, err)
	}
	if result.Status != "preflight-failed" || result.Receipt.Dispatch != "" || backend.actionCalls != 0 {
		t.Fatalf("result=%+v actionCalls=%d, want no dispatch", result, backend.actionCalls)
	}
}

func TestAccessibilityCLIChangedVerificationRejectsRootIdentitySwap(t *testing.T) {
	backend := &cliVerifiedActionFake{changeRootAfterAction: true}
	factory := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
		}
	}
	stdout, stderr, code := captureRunIO(t, []string{
		"a11y", "action", "--app", "Test", "--role", "button", "--name", "Save",
		"--expect-state", "changed", "--timeout", "1s", "--json",
	}, factory)
	if code == 0 || !strings.Contains(stderr, "root identity changed during verification") {
		t.Fatalf("code=%d stderr=%q stdout=%q, want scope swap to fail verification", code, stderr, stdout)
	}
	var result accessibilityActionCLIResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("scope failure output is not JSON: %q: %v", stdout, err)
	}
	if result.Status != "verification-failed" || result.Receipt.Dispatch != accessibility.DispatchAccepted || result.Receipt.Outcome.Status != perfuncted.ActionOutcomeNotVerified || backend.actionCalls != 1 {
		t.Fatalf("result=%+v actionCalls=%d, want accepted but unverified one-shot action", result, backend.actionCalls)
	}
}

func TestAccessibilityCLIVerificationFlagsValidateBeforeOpeningSession(t *testing.T) {
	opened := 0
	factory := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opened++
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAutomationFake{}), nil
		}
	}
	_, stderr, code := captureRunIO(t, []string{
		"a11y", "action", "--app", "Test", "--role", "button", "--expect-text", "Saved", "--timeout", "0s",
	}, factory)
	if code == 0 || !strings.Contains(stderr, "--timeout must be positive") || opened != 0 {
		t.Fatalf("code=%d stderr=%q sessionsOpened=%d, want invalid request rejected before session", code, stderr, opened)
	}
}

func TestAccessibilityCLIWindowOnlyScopeUsesCanonicalResolver(t *testing.T) {
	for _, scope := range [][]string{
		{"--window-id", "managed-window"},
		{"--window", "Test window"},
	} {
		scope := scope
		t.Run(strings.Join(scope, "-"), func(t *testing.T) {
			args := append([]string{"a11y", "tree"}, scope...)
			stdout, stderr, code := captureRunIO(t, args, openCLIWithWindowAndAccessibility)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
			}
			if !strings.Contains(stdout, "/window") {
				t.Fatalf("window scope output = %q, want canonical window root", stdout)
			}
		})
	}
}

func TestAccessibilityCLIScopeSessionRequiresWindowsOnlyForWindowSelectors(t *testing.T) {
	var requests [][]perfuncted.Capability
	factory := func(cfg *cliConfig) sessionOpener {
		requests = append(requests, slices.Clone(cfg.required))
		return func(context.Context) (*perfuncted.Session, error) {
			if slices.Contains(cfg.required, perfuncted.CapabilityWindows) {
				manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "managed-window", Title: "Test window"}}}}
				return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil, cliAutomationFake{}), nil
			}
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAutomationFake{}), nil
		}
	}
	_, _, code := captureRunIO(t, []string{"a11y", "tree", "--window-id", "managed-window"}, factory)
	if code != 0 || len(requests) != 1 || !slices.Equal(requests[0], []perfuncted.Capability{perfuncted.CapabilityAccessibility, perfuncted.CapabilityWindows}) {
		t.Fatalf("window selector requests=%v code=%d, want Accessibility+Windows", requests, code)
	}
	requests = nil
	_, _, code = captureRunIO(t, []string{"a11y", "tree", "--app", "Test"}, factory)
	if code != 0 || len(requests) != 1 || !slices.Equal(requests[0], []perfuncted.Capability{perfuncted.CapabilityAccessibility}) {
		t.Fatalf("app selector requests=%v code=%d, want Accessibility only", requests, code)
	}
}

func TestAccessibilityCLISemanticActionRejectsAmbiguity(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "action", "--app", "Test", "--role", "button"}, openCLIWithAmbiguousAccessibility)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "ambiguous") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAccessibilityCLIHelpKeepsProtocolCommandsUnderRaw(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "--help"}, openCLIWithAccessibility)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	for _, want := range []string{"action", "apps", "tree", "raw"} {
		if !strings.Contains(stdout, "  "+want) {
			t.Fatalf("help=%q, missing primary command %q", stdout, want)
		}
	}
	for _, old := range []string{"\n  applications ", "\n  snapshot ", "\n  invoke-action "} {
		if strings.Contains(stdout, old) {
			t.Fatalf("help still exposes protocol-shaped command %q: %s", old, stdout)
		}
	}
}

func TestAccessibilityCLIRawRequiresExplicitGeneration(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "raw", "--op", "action", "--bus", "org.test", "--path", "/node"}, openCLIWithAutomation)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "--generation") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestParseAccessibilityCoordType(t *testing.T) {
	for _, test := range []struct {
		input string
		want  accessibility.CoordType
	}{
		{"screen", accessibility.CoordTypeScreen},
		{"WINDOW", accessibility.CoordTypeWindow},
		{" parent ", accessibility.CoordTypeParent},
	} {
		got, err := parseAccessibilityCoordType(test.input)
		if err != nil || got != test.want {
			t.Fatalf("parse %q = %v, %v; want %v", test.input, got, err, test.want)
		}
	}
	if _, err := parseAccessibilityCoordType("alignment"); err == nil {
		t.Fatal("invalid coordinate space unexpectedly accepted")
	}
}

func openCLIWithAccessibility(*cliConfig) sessionOpener {
	return func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAccessibilityFake{}), nil
	}
}

func openCLIWithEvents(*cliConfig) sessionOpener {
	return func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliEventsFake{}), nil
	}
}

func openCLIWithAutomation(*cliConfig) sessionOpener {
	return func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAutomationFake{}), nil
	}
}

func openCLIWithAutomationOperations(operations []string) func(*cliConfig) sessionOpener {
	return func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			backend := cliAutomationCapabilitiesFake{operations: operations}
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
		}
	}
}

func openCLIWithWindowAndAccessibility(*cliConfig) sessionOpener {
	return func(context.Context) (*perfuncted.Session, error) {
		manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "managed-window", Title: "Test window"}}}}
		return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil, cliAutomationFake{}), nil
	}
}

func openCLIWithAmbiguousAccessibility(*cliConfig) sessionOpener {
	return func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAmbiguousFake{}), nil
	}
}

func TestAccessibilityCLICommandsJSON(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "tree", args: []string{"a11y", "tree", "--app", "Test", "--json", "--max-depth", "2"}, want: "root"},
		{name: "find", args: []string{"a11y", "find", "--app", "Test", "--json", "--role", "button", "--name", "Save"}, want: "Save"},
		{name: "focused", args: []string{"a11y", "focused", "--json"}, want: "focused"},
		{name: "at point", args: []string{"a11y", "at-point", "--json", "--x", "10", "--y", "20"}, want: "point"},
		{name: "at point positional", args: []string{"a11y", "at-point", "--json", "10", "20"}, want: "point"},
		{name: "events", args: []string{"a11y", "events"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := captureRunIO(t, tt.args, openCLIWithAccessibility)
			if code != 0 || stderr != "" {
				t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
			}
			if !strings.Contains(stdout, tt.want) {
				t.Fatalf("stdout=%q, want substring %q", stdout, tt.want)
			}
		})
	}
}

func TestAccessibilityCLIRejectsPartialRoot(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "tree"}, openCLIWithAccessibility)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "explicit --app") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAccessibilityCLIAutomationCommands(t *testing.T) {
	tests := [][]string{
		{"a11y", "raw", "--op", "action", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"a11y", "raw", "--op", "focus", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"a11y", "raw", "--op", "scroll", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"a11y", "raw", "--op", "set-value", "--bus", "org.test", "--path", "/node", "--generation", "1", "--value", "0.5"},
		{"a11y", "raw", "--op", "set-text-contents", "--bus", "org.test", "--path", "/node", "--generation", "1", "--text", "updated"},
		{"a11y", "raw", "--op", "set-text-selection", "--bus", "org.test", "--path", "/node", "--generation", "1", "--selection", "0", "--start", "0", "--end", "1"},
		{"a11y", "raw", "--op", "add-text-selection", "--bus", "org.test", "--path", "/node", "--generation", "1", "--start", "0", "--end", "1"},
		{"a11y", "raw", "--op", "remove-text-selection", "--bus", "org.test", "--path", "/node", "--generation", "1", "--selection", "0"},
		{"a11y", "raw", "--op", "select-child", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"a11y", "raw", "--op", "select-all", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"a11y", "raw", "--op", "clear-selection", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"a11y", "raw", "--op", "select-row", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"a11y", "raw", "--op", "deselect-row", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"a11y", "raw", "--op", "select-column", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"a11y", "raw", "--op", "deselect-column", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"a11y", "raw", "--op", "reopen"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args[1:], "-"), func(t *testing.T) {
			stdout, stderr, code := captureRunIO(t, args, openCLIWithAutomation)
			if code != 0 || stderr != "" {
				t.Fatalf("args=%v code=%d stderr=%q stdout=%q", args, code, stderr, stdout)
			}
		})
	}
}

func TestAccessibilityCLIRawActionUsesSpecificCapability(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		capability string
	}{
		{
			name:       "default action",
			capability: "invoke-default-action",
		},
		{
			name:       "indexed action",
			args:       []string{"--action-index", "1"},
			capability: "invoke-action",
		},
		{
			name:       "named action",
			args:       []string{"--action-name", "press"},
			capability: "invoke-action-by-name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"a11y", "raw", "--op", "action", "--bus", "org.test", "--path", "/node", "--generation", "1"}
			args = append(args, tt.args...)
			open := openCLIWithAutomationOperations([]string{tt.capability})
			stdout, stderr, code := captureRunIO(t, args, open)
			if code != 0 || stderr != "" {
				t.Fatalf("args=%v code=%d stderr=%q stdout=%q", args, code, stderr, stdout)
			}
		})
	}
}

func TestAccessibilityCLIReopenJSONOutput(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"a11y", "raw", "--op", "reopen", "--json"}, openCLIWithAutomation)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, `"ok"`) && !strings.Contains(stdout, "ok") {
		t.Fatalf("expected JSON ok output, got stdout=%q", stdout)
	}
}

func TestParseAccessibilityAttributes(t *testing.T) {
	got := parseAccessibilityAttributes([]string{"kind=Primary", " aria-label = Save = now", "invalid", "=empty"})
	if got["kind"] != "Primary" || got["aria-label"] != "Save = now" {
		t.Fatalf("attributes = %#v", got)
	}
	if _, ok := got["invalid"]; ok {
		t.Fatal("invalid attribute was accepted")
	}
}

// Every valid CLI operation must stop at an unavailable accessibility session
// rather than emitting fabricated success or reaching a privileged primitive.
func TestAccessibilityCLIValidCommandsPropagateAuthorizationDenial(t *testing.T) {
	denied := errors.New("synthetic AT-SPI authorization denied")
	for _, tc := range []struct {
		name   string
		args   []string
		window bool
	}{
		{"apps", []string{"apps"}, false},
		{"tree scoped", []string{"tree", "--app", "Editor"}, false},
		{"find scoped", []string{"find", "--app", "Editor", "--name", "Save"}, false},
		{"focused", []string{"focused"}, false},
		{"at point default", []string{"at-point"}, false},
		{"at point coordinates", []string{"at-point", "14", "18"}, false},
		{"events", []string{"events", "--buffer", "2"}, false},
		{"semantic action", []string{"action", "--app", "Editor", "--role", "button"}, false},
		{"semantic focus", []string{"focus", "--app", "Editor", "--role", "textbox"}, false},
		{"semantic text", []string{"text", "--app", "Editor", "--role", "entry", "--value", "new"}, false},
		{"raw focus", []string{"raw", "--op", "focus", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}, false},
		{"raw reopen", []string{"raw", "--op", "reopen"}, false},
		{"window scoped tree", []string{"tree", "--window-id", "123"}, true},
		{"window scoped action", []string{"action", "--window", "Editor", "--role", "button"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			direct, windowOpens := 0, 0
			mainOpen := func(context.Context) (*perfuncted.Session, error) { direct++; return nil, denied }
			windowOpen := func(context.Context) (*perfuncted.Session, error) { windowOpens++; return nil, denied }
			cmd := accessibilityCmd(mainOpen, windowOpen)
			// Production's root command suppresses Cobra usage text on
			// operational failures. Preserve that contract in this direct harness.
			cmd.SilenceUsage = true
			var out, errOut bytes.Buffer
			cmd.SetArgs(tc.args)
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			err := cmd.ExecuteContext(context.Background())
			if !errors.Is(err, denied) {
				t.Fatalf("authorization failure lost: %v", err)
			}
			if out.Len() != 0 {
				t.Fatalf("denied action published result: %q", out.String())
			}
			if tc.window {
				if direct != 0 || windowOpens != 1 {
					t.Fatalf("wrong session opened: main=%d window=%d", direct, windowOpens)
				}
			} else if direct != 1 || windowOpens != 0 {
				t.Fatalf("wrong session opened: main=%d window=%d", direct, windowOpens)
			}
		})
	}
}

func TestAccessibilityAtPointRejectsMalformedCoordinatesBeforeOpeningSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"one coordinate", []string{"1"}, "expects zero or two coordinates"},
		{"invalid x", []string{"left", "2"}, "at-point x"},
		{"invalid y", []string{"1", "down"}, "at-point y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			args := append([]string{"a11y", "at-point"}, tc.args...)
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("unexpected session open")
				}
			})
			if code == 0 || out != "" || !strings.Contains(stderr, tc.want) || opens != 0 {
				t.Fatalf("malformed coordinates reached session: args=%v code=%d stdout=%q stderr=%q opens=%d", args, code, out, stderr, opens)
			}
		})
	}
}

func TestAccessibilityWindowTitleAndIDConflictFailsBeforeATSPICorrelation(t *testing.T) {
	opens := 0
	args := []string{"a11y", "tree", "--window", "Editor", "--window-id", "different-id"}
	out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opens++
			manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "managed-id", Title: "Editor"}}}}
			return perfuncted.NewSessionForTesting(nil, nil, manager, nil, nil, cliAutomationFake{}), nil
		}
	})
	if code == 0 || out != "" || !strings.Contains(stderr, "identify different managed windows") || opens != 1 {
		t.Fatalf("conflicting managed window selectors accepted: code=%d stdout=%q stderr=%q opens=%d", code, out, stderr, opens)
	}
}

// An empty document-selection batch cannot be applied by the native AT-SPI
// backend. Reject it before desktop authorization rather than opening a
// session only to discover a known-invalid request after connecting.
func TestAccessibilityEmptyDocumentSelectionsRejectBeforeDesktopAuthorization(t *testing.T) {
	for _, input := range []string{"[]", "null", " [] ", "\nnull"} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			opens := 0
			args := []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/editor", "--generation", "1", "--op", "set-document-text-selections", "--selections", input}
			stdout, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return nil, errors.New("desktop authorization reached")
				}
			})
			if code == 0 || opens != 0 || stdout != "" || !strings.Contains(stderr, "must not be empty") {
				t.Fatalf("empty selection=%q: opened=%d code=%d stdout=%q stderr=%q", input, opens, code, stdout, stderr)
			}
		})
	}
}

// Explicitly negative event buffering is invalid user input, not a request
// for a default. Refuse before acquiring accessibility authorization.
func TestAccessibilityEventsRejectNegativeBufferBeforeSession(t *testing.T) {
	for _, v := range []string{"-1", "-4096"} {
		t.Run(v, func(t *testing.T) {
			opened := 0
			_, stderr, exit := captureRunIO(t, []string{"a11y", "events", "--buffer=" + v}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, nil }
			})
			if opened != 0 {
				t.Fatalf("negative buffer opened accessibility session %d times", opened)
			}
			if exit == 0 || !strings.Contains(stderr, "buffer") {
				t.Fatalf("exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestAccessibilityEventsPropagateOutputFailure(t *testing.T) {
	failure := errors.New("event output consumer failed")
	out := &screenCommandFailureWriter{failAt: 1, failure: failure}
	cmd := accessibilityCmd(func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliEventsFake{}), nil
	}, nil)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(out)
	cmd.SetArgs([]string{"events"})
	err := cmd.ExecuteContext(context.Background())
	if !errors.Is(err, failure) || out.writes != 1 {
		t.Fatalf("event output error=%v writes=%d, want original writer failure", err, out.writes)
	}
}

// AT-SPI node handles must be real D-Bus object paths. Malformed paths are
// statically invalid and must not trigger a desktop authorization prompt.
func TestAccessibilityRawMalformedObjectPathsNeverOpenDesktopSession(t *testing.T) {
	for _, path := range []string{"node", "/node//child", "/node/../child", "/node-with-hyphen"} {
		t.Run(path, func(t *testing.T) {
			opened := 0
			args := []string{"a11y", "raw", "--op", "focus", "--bus", "org.example.App", "--path", path, "--generation", "1"}
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unexpected desktop access")
				}
			})
			if code == 0 || opened != 0 || out != "" || !strings.Contains(stderr, "valid D-Bus object path") {
				t.Fatalf("invalid path %q opened=%d code=%d stdout=%q stderr=%q", path, opened, code, out, stderr)
			}
		})
	}
}

// Invalid process IDs must not trigger AT-SPI authorization or access.
func TestAccessibilityNegativePIDFailsBeforeOpeningSession(t *testing.T) {
	for _, args := range [][]string{
		{"a11y", "tree", "--pid=-1"},
		{"a11y", "find", "--pid=-50"},
		{"a11y", "action", "--pid=-2", "--action", "press"},
		{"a11y", "focus", "--pid=-3"},
		{"a11y", "text", "--pid=-4"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, errText, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, nil
				}
			})
			if opened != 0 {
				t.Fatalf("opened session %d times for invalid PID", opened)
			}
			if code == 0 || !strings.Contains(strings.ToLower(errText), "pid") {
				t.Fatalf("code=%d stderr=%q", code, errText)
			}
		})
	}
}

func TestAccessibilityNegativeSnapshotBoundsFailBeforeSession(t *testing.T) {
	for _, tc := range []struct {
		flag    string
		command string
	}{
		{"--max-depth=-1", "tree"},
		{"--max-nodes=-2", "find"},
		{"--max-text-bytes=-3", "action"},
		{"--max-total-bytes=-4", "focus"},
		{"--max-depth=-5", "text"},
	} {
		t.Run(tc.command+" "+tc.flag, func(t *testing.T) {
			args := []string{"a11y", tc.command, "--app", "Example", tc.flag}
			count := 0
			_, stderr, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { count++; return nil, nil }
			})
			if count != 0 {
				t.Fatalf("opened desktop session %d times for invalid limit", count)
			}
			if exit == 0 || !strings.Contains(stderr, "--max-") {
				t.Fatalf("exit=%d stderr=%q", exit, stderr)
			}
		})
	}
}

func TestAccessibilityAtPointRejectsOutOfRangeCoordinatesBeforeOpen(t *testing.T) {
	cases := [][]string{
		{"a11y", "at-point", "2147483648", "0"},
		{"a11y", "at-point", "--", "0", "-2147483649"},
		{"a11y", "at-point", "--x=2147483648"},
		{"a11y", "at-point", "--y=-2147483649"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, errText, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { opened++; return nil, nil }
			})
			if opened != 0 {
				t.Fatalf("opened accessibility session for out-of-range point: %d", opened)
			}
			if code == 0 || !strings.Contains(errText, "range") {
				t.Fatalf("code=%d stderr=%q", code, errText)
			}
		})
	}
}

func TestAccessibilityAtPointAcceptsSigned32BitBoundaryCoordinates(t *testing.T) {
	for _, args := range [][]string{
		{"a11y", "at-point", "--x=2147483647", "--y=-2147483648"},
		{"a11y", "at-point", "--", "-2147483648", "2147483647"},
		{"a11y", "at-point"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			opened := 0
			_, _, _ = captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("synthetic session opener reached")
				}
			})
			if opened != 1 {
				t.Fatalf("valid 32-bit coordinate should reach session opener once, got %d", opened)
			}
		})
	}
}

// Raw operations are a defense-in-depth boundary: even direct dispatch must
// reject malformed local parameters before touching an accessibility backend.
func TestRawAccessibilityDispatchRejectsMalformedInputsBeforeBackend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request rawInvocation
		message string
	}{
		{"scroll alignment", rawInvocation{op: "scroll", alignment: "bogus-alignment"}, "unknown alignment"},
		{"scroll point space", rawInvocation{op: "scroll-to-point", coord: "bogus-space"}, "unknown coordinate space"},
		{"position space", rawInvocation{op: "set-position", coord: "bogus-space"}, "unknown coordinate space"},
		{"extents space", rawInvocation{op: "set-extents", coord: "bogus-space"}, "unknown coordinate space"},
		{"malformed selections", rawInvocation{op: "set-document-text-selections", selectionsJSON: "{not json"}, "invalid --selections JSON"},
		{"unknown operation", rawInvocation{op: "unrecognized-op"}, "unknown raw op"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := invokeRawOp(context.Background(), nil, accessibility.NodeID{}, tc.request)
			if value != nil || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("malformed request returned value=%v err=%v (want %q)", value, err, tc.message)
			}
		})
	}
}

func TestAccessibilityRawMalformedRequestNeverOpensAuthorizationSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing operation", []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}},
		{"missing bus", []string{"a11y", "raw", "--op", "focus", "--path", "/node", "--generation", "1"}},
		{"missing path", []string{"a11y", "raw", "--op", "focus", "--bus", "org.test.App", "--generation", "1"}},
		{"missing generation", []string{"a11y", "raw", "--op", "focus", "--bus", "org.test.App", "--path", "/node"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unnecessary D-Bus authorization")
				}
			})
			if code == 0 || opened != 0 || strings.Contains(errOut, "unnecessary D-Bus") || out != "" {
				t.Fatalf("malformed request %v accessed desktop %d times: code=%d out=%q err=%q", tc.args, opened, code, out, errOut)
			}
		})
	}
}

func TestAccessibilityRawInvalidTypedOperationNeverAcquiresSession(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown raw operation", []string{"--op", "not-an-operation"}},
		{"invalid scroll alignment", []string{"--op", "scroll", "--alignment", "wrong"}},
		{"invalid scroll coordinate", []string{"--op", "scroll-to-point", "--coordinate-space", "wrong"}},
		{"invalid position coordinate", []string{"--op", "set-position", "--coordinate-space", "wrong"}},
		{"invalid extents coordinate", []string{"--op", "set-extents", "--coordinate-space", "wrong"}},
		{"invalid document selections JSON", []string{"--op", "set-document-text-selections", "--selections", "{"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			args := append([]string{"a11y", "raw", "--bus", "org.example.App", "--path", "/node", "--generation", "1"}, tc.args...)
			out, errOut, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opened++
					return nil, errors.New("unexpected authorization")
				}
			})
			if opened != 0 || code == 0 || out != "" || strings.Contains(errOut, "unexpected authorization") {
				t.Fatalf("invalid typed op %v triggered %d sessions: code=%d stdout=%q stderr=%q", args, opened, code, out, errOut)
			}
		})
	}
}

// Incomplete semantic requests must not initialize the AT-SPI desktop,
// especially when opening the session may trigger user authorization.
func TestAccessibilitySemanticCommandsRequireScopeBeforeOpeningSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"tree", []string{"a11y", "tree"}},
		{"find", []string{"a11y", "find", "--name", "Save"}},
		{"action", []string{"a11y", "action", "--role", "button", "--action", "press"}},
		{"focus", []string{"a11y", "focus", "--role", "textbox"}},
		{"text", []string{"a11y", "text", "--role", "entry", "--value", "confidential"}},
		{"empty app scope", []string{"a11y", "tree", "--app", "   "}},
		{"empty window scope", []string{"a11y", "focus", "--window", " "}},
		{"explicit zero PID", []string{"a11y", "find", "--pid", "0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			root := newRootCmd(func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					attempts++
					return nil, errors.New("unexpected accessibility session authorization")
				}
			})
			var out, errOut bytes.Buffer
			root.SetArgs(tc.args)
			root.SetOut(&out)
			root.SetErr(&errOut)
			err := root.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "explicit --app") {
				t.Fatalf("missing scope returned %v", err)
			}
			if attempts != 0 {
				t.Fatalf("scope-free request accessed desktop %d times", attempts)
			}
			if out.Len() != 0 {
				t.Fatalf("unscoped request produced output: %q", out.String())
			}
		})
	}
}

type cancelOnIdleEventSubscription struct {
	cliAccessibilityFake
	subscribed chan<- struct{}
}

// The provider accepts a live subscription but may emit no invalidation
// events. Cancellation must still end the public command immediately without
// waiting for a message or fabricating a successful stream completion.
func (p cancelOnIdleEventSubscription) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	events := make(chan accessibility.Event)
	close(p.subscribed)
	return events, nil
}

func TestAccessibilityEventsCancelWhileSubscriptionIsIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	subscribed := make(chan struct{})
	provider := cancelOnIdleEventSubscription{subscribed: subscribed}
	command := accessibilityCmd(func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, provider), nil
	}, nil)
	command.SilenceErrors = true
	command.SilenceUsage = true
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"events"})
	result := make(chan error, 1)
	go func() { result <- command.ExecuteContext(ctx) }()
	<-subscribed
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled idle accessibility events returned %v, want cancellation", err)
	}
	if output.Len() != 0 {
		t.Fatalf("idle canceled stream emitted %q", output.String())
	}
}

type failAccessibilityNthWrite struct {
	writes   int
	failAt   int
	err      error
	accepted bytes.Buffer
}

func (w *failAccessibilityNthWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return w.accepted.Write(p)
}

// A partially delivered outline must not be acknowledged as complete. The
// first failed descendant write must stop recursion without leaking siblings
// or publishing the successful parent as an authoritative full tree.
func TestAccessibilityOutlineStopsOnNestedOutputFailure(t *testing.T) {
	sentinel := errors.New("synthetic output stream interrupted")
	root := accessibility.OutlineNode{Role: "application", Name: "Editor", Children: []accessibility.OutlineNode{
		{Role: "dialog", Name: "Preferences", Children: []accessibility.OutlineNode{{Role: "button", Name: "Save"}}},
		{Role: "entry", Name: "Private sibling"},
	}}
	for _, tc := range []struct {
		name        string
		failAt      int
		mustHave    []string
		mustNotHave []string
	}{
		{"first child", 2, []string{"Editor"}, []string{"Preferences", "Save", "Private sibling"}},
		{"grandchild", 3, []string{"Editor", "Preferences"}, []string{"Save", "Private sibling"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &failAccessibilityNthWrite{failAt: tc.failAt, err: sentinel}
			if err := accessibilityOutput(w, false, accessibility.Outline{Root: root}); !errors.Is(err, sentinel) {
				t.Fatalf("nested write failure suppressed: %v", err)
			}
			if w.writes != tc.failAt {
				t.Fatalf("wrote %d nodes after sink failure at %d", w.writes, tc.failAt)
			}
			for _, s := range tc.mustHave {
				if !strings.Contains(w.accepted.String(), s) {
					t.Fatalf("missing expected delivered parent %q: %q", s, w.accepted.String())
				}
			}
			for _, s := range tc.mustNotHave {
				if strings.Contains(w.accepted.String(), s) {
					t.Fatalf("undelivered private descendant %q emitted: %q", s, w.accepted.String())
				}
			}
		})
	}
}

// A semantic scope request must be rejected without querying providers even
// when called directly with an already-open session rather than through Cobra.
func TestAccessibilityOptionsRootRejectsMissingScopeBeforeBackend(t *testing.T) {
	_, err := (accessibilityCLIOptions{}).root(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "explicit --app") {
		t.Fatalf("missing scope returned %v", err)
	}
}

type rejectingAccessibilityWriter struct{ err error }

func (w rejectingAccessibilityWriter) Write([]byte) (int, error) { return 0, w.err }

// A failed output sink must interrupt every format, including nested outlines
// and action receipts; do not acknowledge success after partial serialization.
func TestAccessibilityOutputNeverAcknowledgesFailedSink(t *testing.T) {
	sentinel := errors.New("synthetic output transport disconnected")
	node := accessibility.Node{ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/node", Generation: 7}, Role: "button", Name: "Save"}
	outline := accessibility.Outline{Root: accessibility.OutlineNode{ID: node.ID, Role: "application", Children: []accessibility.OutlineNode{{ID: node.ID, Role: "button", Name: "Save"}}}}
	for _, tc := range []struct {
		name  string
		value any
		json  bool
	}{
		{"applications", []accessibility.Application{{Node: node, PID: 42}}, false},
		{"node", node, false},
		{"nodes", []accessibility.Node{node, node}, false},
		{"snapshot", accessibility.Snapshot{Root: node, Nodes: []accessibility.Node{node}, Generation: 7}, false},
		{"outline", outline, false},
		{"partial outline", accessibility.Outline{Generation: 7, Truncated: true, Root: accessibility.OutlineNode{ID: node.ID, Role: "app"}}, false},
		{"action", accessibility.Action{Index: 4, Name: "press"}, false},
		{"receipt", perfuncted.AccessibilityActionReceipt{Node: node, Action: accessibility.Action{Index: 4, Name: "press"}}, false},
		{"fallback", struct{ Value int }{Value: 19}, false},
		{"JSON", node, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := accessibilityOutput(rejectingAccessibilityWriter{err: sentinel}, tc.json, tc.value)
			if !errors.Is(err, sentinel) {
				t.Fatalf("failed sink acknowledged: %v", err)
			}
		})
	}
}

func TestAccessibilitySessionOpenerPropagatesFailedAuthorization(t *testing.T) {
	denied := errors.New("synthetic inaccessible AT-SPI bus")
	opens := 0
	for _, tc := range []struct {
		name string
		open sessionOpener
		want string
	}{
		{"provider denied", func(context.Context) (*perfuncted.Session, error) { opens++; return nil, denied }, "denied"},
		{"nil session", func(context.Context) (*perfuncted.Session, error) { opens++; return nil, nil }, "capability unavailable"},
		{"session missing accessibility", func(context.Context) (*perfuncted.Session, error) {
			opens++
			return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil), nil
		}, "capability unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, err := openAccessibilitySession(context.Background(), tc.open)
			if session != nil || err == nil {
				t.Fatalf("denied session accepted: session=%v err=%v", session, err)
			}
			if tc.name == "provider denied" && !errors.Is(err, denied) {
				t.Fatalf("provider denial lost: %v", err)
			}
		})
	}
	if opens != 3 {
		t.Fatalf("expected exactly one open per scenario: %d", opens)
	}
}

type deniedAccessibilityReads struct {
	cliAccessibilityFake
	failure error
	calls   int
}

func (f *deniedAccessibilityReads) Applications(context.Context) ([]accessibility.Application, error) {
	f.calls++
	return nil, f.failure
}
func (f *deniedAccessibilityReads) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	f.calls++
	return accessibility.Application{}, f.failure
}
func (f *deniedAccessibilityReads) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.calls++
	return accessibility.Snapshot{}, f.failure
}
func (f *deniedAccessibilityReads) Find(context.Context, accessibility.NodeID, accessibility.Query, accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	f.calls++
	return nil, f.failure
}
func (f *deniedAccessibilityReads) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	f.calls++
	return accessibility.Node{}, f.failure
}
func (f *deniedAccessibilityReads) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	f.calls++
	return accessibility.Node{}, f.failure
}
func (f *deniedAccessibilityReads) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	f.calls++
	return nil, f.failure
}

// Provider failures must never be mistaken for an empty tree or a negative
// query result. Every read command stops at the first failed provider call.
func TestAccessibilityReadCommandsReturnProviderFailuresWithoutResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"apps", []string{"a11y", "apps"}},
		{"tree application resolution", []string{"a11y", "tree", "--app", "Editor"}},
		{"find application resolution", []string{"a11y", "find", "--app", "Editor", "--role", "button"}},
		{"focused", []string{"a11y", "focused"}},
		{"at point", []string{"a11y", "at-point", "10", "20"}},
		{"events", []string{"a11y", "events"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &deniedAccessibilityReads{failure: errors.New("synthetic inaccessible provider")}
			opens := 0
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, f), nil
				}
			})
			if exit == 0 || stdout != "" || !strings.Contains(stderr, f.failure.Error()) || opens != 1 || f.calls != 1 {
				t.Fatalf("provider failure swallowed args=%v exit=%d stdout=%q stderr=%q opens=%d provider_calls=%d", tc.args, exit, stdout, stderr, opens, f.calls)
			}
		})
	}
}

type recordingATSPIMutations struct {
	cliAutomationFake
	calls   []string
	failure error
}

func (b *recordingATSPIMutations) ScrollToPoint(_ context.Context, _ accessibility.NodeID, coord accessibility.CoordType, x, y int) error {
	b.calls = append(b.calls, fmt.Sprintf("scroll-point:%d:%d:%d", coord, x, y))
	return b.failure
}
func (b *recordingATSPIMutations) SetPosition(_ context.Context, _ accessibility.NodeID, x, y int, coord accessibility.CoordType) error {
	b.calls = append(b.calls, fmt.Sprintf("position:%d:%d:%d", coord, x, y))
	return b.failure
}
func (b *recordingATSPIMutations) SetSize(_ context.Context, _ accessibility.NodeID, w, h int) error {
	b.calls = append(b.calls, fmt.Sprintf("size:%d:%d", w, h))
	return b.failure
}
func (b *recordingATSPIMutations) SetExtents(_ context.Context, _ accessibility.NodeID, x, y, w, h int, coord accessibility.CoordType) error {
	b.calls = append(b.calls, fmt.Sprintf("extents:%d:%d:%d:%d:%d", coord, x, y, w, h))
	return b.failure
}
func (b *recordingATSPIMutations) InsertText(_ context.Context, _ accessibility.NodeID, offset int32, value string) error {
	b.calls = append(b.calls, fmt.Sprintf("insert:%d:%s", offset, value))
	return b.failure
}
func (b *recordingATSPIMutations) DeleteText(_ context.Context, _ accessibility.NodeID, start, end int32) error {
	b.calls = append(b.calls, fmt.Sprintf("delete:%d:%d", start, end))
	return b.failure
}
func (b *recordingATSPIMutations) CopyText(_ context.Context, _ accessibility.NodeID, start, end int32) error {
	b.calls = append(b.calls, fmt.Sprintf("copy:%d:%d", start, end))
	return b.failure
}
func (b *recordingATSPIMutations) CutText(_ context.Context, _ accessibility.NodeID, start, end int32) error {
	b.calls = append(b.calls, fmt.Sprintf("cut:%d:%d", start, end))
	return b.failure
}
func (b *recordingATSPIMutations) PasteText(_ context.Context, _ accessibility.NodeID, position int32) error {
	b.calls = append(b.calls, fmt.Sprintf("paste:%d", position))
	return b.failure
}
func (b *recordingATSPIMutations) SetCaretOffset(_ context.Context, _ accessibility.NodeID, offset int32) error {
	b.calls = append(b.calls, fmt.Sprintf("caret:%d", offset))
	return b.failure
}
func (b *recordingATSPIMutations) SetTextSelections(_ context.Context, _ accessibility.NodeID, selections []accessibility.DocumentTextSelection) error {
	b.calls = append(b.calls, fmt.Sprintf("selection-count:%d", len(selections)))
	return b.failure
}
func (b *recordingATSPIMutations) DeselectChild(_ context.Context, _ accessibility.NodeID, index int32) error {
	b.calls = append(b.calls, fmt.Sprintf("deselect-child:%d", index))
	return b.failure
}
func (b *recordingATSPIMutations) DeselectSelectedChild(_ context.Context, _ accessibility.NodeID) error {
	b.calls = append(b.calls, "deselect-selected-child")
	return b.failure
}

func TestAccessibilityRawCLIRoutesEveryRemainingMutationToCorrectBackendMethod(t *testing.T) {
	base := []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}
	cases := []struct {
		name, op string
		args     []string
		want     string
	}{
		{"scroll point in window coordinates", "scroll-to-point", []string{"--coordinate-space", "window", "--x", "123", "--y", "456"}, "scroll-point:1:123:456"},
		{"scroll point in screen coordinates", "scroll-to-point", []string{"--coordinate-space", "screen", "--x", "13", "--y", "25"}, "scroll-point:0:13:25"},
		{"position in parent coordinates", "set-position", []string{"--coordinate-space", "parent", "--x", "9", "--y", "7"}, "position:2:9:7"},
		{"size", "set-size", []string{"--width", "111", "--height", "222"}, "size:111:222"},
		{"extents", "set-extents", []string{"--coordinate-space", "window", "--x", "1", "--y", "2", "--width", "3", "--height", "4"}, "extents:1:1:2:3:4"},
		{"insert range-text overrides text", "insert-text", []string{"--offset", "6", "--text", "ignored", "--range-text", "actual"}, "insert:6:actual"},
		{"insert text fallback", "insert-text", []string{"--offset", "7", "--text", "fallback"}, "insert:7:fallback"},
		{"delete", "delete-text", []string{"--start", "2", "--end", "8"}, "delete:2:8"},
		{"copy", "copy-text", []string{"--start", "5", "--end", "15"}, "copy:5:15"},
		{"cut", "cut-text", []string{"--start", "9", "--end", "19"}, "cut:9:19"},
		{"paste", "paste-text", []string{"--position", "22"}, "paste:22"},
		{"caret", "set-caret", []string{"--offset", "18"}, "caret:18"},
		{"document selection with one valid text range", "set-document-text-selections", []string{"--selections", `[{"startObject":{"busName":"org.test.App","objectPath":"/entry","generation":1},"startOffset":1,"endObject":{"busName":"org.test.App","objectPath":"/entry","generation":1},"endOffset":4}]`}, "selection-count:1"},
		{"deselect child", "deselect-child", []string{"--index", "7"}, "deselect-child:7"},
		{"deselect selected child", "deselect-selected-child", nil, "deselect-selected-child"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &recordingATSPIMutations{}
			args := append(append(append([]string{}, base...), "--op", tc.op), tc.args...)
			out, errOut, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
				}
			})
			if exit != 0 || strings.TrimSpace(out) != "ok" || errOut != "" {
				t.Fatalf("raw %v exit=%d stdout=%q stderr=%q", args, exit, out, errOut)
			}
			if len(backend.calls) != 1 || backend.calls[0] != tc.want {
				t.Fatalf("raw %s dispatched %v, want %s", tc.name, backend.calls, tc.want)
			}
		})
	}
}
func TestAccessibilityRawCLIRejectsUnknownOrMalformedTypedParameters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown op", []string{"--op", "nonexistent"}, "unknown raw op"},
		{"bad coordinate type", []string{"--op", "scroll-to-point", "--coordinate-space", "planet"}, "unknown coordinate space"},
		{"bad position coordinate", []string{"--op", "set-position", "--coordinate-space", "planet"}, "unknown coordinate space"},
		{"bad extents coordinate", []string{"--op", "set-extents", "--coordinate-space", "planet"}, "unknown coordinate space"},
		{"bad scroll alignment", []string{"--op", "scroll", "--alignment", "invalid"}, "unknown alignment"},
		{"bad JSON selection", []string{"--op", "set-document-text-selections", "--selections", "{"}, "invalid --selections JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"a11y", "raw", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}, tc.args...)
			out, errOut, code := captureRunIO(t, args, openCLIWithAutomation)
			if code == 0 || out != "" || !strings.Contains(errOut, tc.want) {
				t.Fatalf("raw %v code=%d stdout=%q stderr=%q", args, code, out, errOut)
			}
		})
	}
}

type atspiFailWriter struct{ err error }

func (w atspiFailWriter) Write([]byte) (int, error) { return 0, w.err }
func TestAccessibilityHumanAndJSONOutputPropagateWriterFailures(t *testing.T) {
	failure := errors.New("synthetic output stream closed")
	id := accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/node", Generation: 9}
	types := []any{
		[]accessibility.Application{{Node: accessibility.Node{ID: id, Name: "Editor", Role: "application"}}},
		accessibility.Node{ID: id, Name: "Editor", Role: "entry"},
		[]accessibility.Node{{ID: id, Name: "Editor", Role: "entry"}},
		accessibility.Snapshot{Root: accessibility.Node{ID: id}, Generation: 9},
		accessibility.Outline{Generation: 9, Truncated: true},
		accessibility.Action{Index: 3, Name: "activate"},
		perfuncted.AccessibilityActionReceipt{Node: accessibility.Node{ID: id}, Generation: 9},
		"default response",
	}
	for i, value := range types {
		t.Run(fmt.Sprintf("type-%d", i), func(t *testing.T) {
			if err := accessibilityOutput(atspiFailWriter{err: failure}, false, value); !errors.Is(err, failure) {
				t.Fatalf("human output dropped writer failure for %T: %v", value, err)
			}
			if err := accessibilityOutput(atspiFailWriter{err: failure}, true, value); !errors.Is(err, failure) {
				t.Fatalf("JSON output dropped writer failure for %T: %v", value, err)
			}
			var buf bytes.Buffer
			if err := accessibilityOutput(&buf, false, value); err != nil {
				t.Fatalf("human output of %T failed: %v", value, err)
			}
			if buf.Len() == 0 && i != 0 {
				t.Fatalf("human output of %T silently empty", value)
			}
		})
	}
}

type resolvedAccessibilityFailureBackend struct {
	cliAutomationFake
	failure   error
	resolves  int
	snapshots int
	finds     int
}

func (f *resolvedAccessibilityFailureBackend) FindApplication(_ context.Context, _ accessibility.ApplicationFilter) (accessibility.Application, error) {
	f.resolves++
	return accessibility.Application{Node: accessibility.Node{ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}, Name: "Editor", Role: "application"}, PID: 42}, nil
}
func (f *resolvedAccessibilityFailureBackend) Snapshot(_ context.Context, _ accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	f.snapshots++
	return accessibility.Snapshot{}, f.failure
}
func (f *resolvedAccessibilityFailureBackend) Find(_ context.Context, _ accessibility.NodeID, _ accessibility.Query, _ accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	f.finds++
	return nil, f.failure
}

// Resolving the requested app is not proof that its contents are readable.
// A failed follow-on read must not serialize a partial tree, match list, or
// acknowledge a semantic operation that has never observed a target.
func TestAccessibilityResolvedScopeProviderFailuresDoNotPublishResults(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		args                     []string
		wantSnapshots, wantFinds int
	}{
		{"tree snapshot", []string{"a11y", "tree", "--app", "Editor"}, 1, 0},
		{"tree JSON snapshot", []string{"a11y", "tree", "--app", "Editor", "--json"}, 1, 0},
		{"find semantic query", []string{"a11y", "find", "--app", "Editor", "--role", "button"}, 1, 0},
		{"find JSON query", []string{"a11y", "find", "--app", "Editor", "--role", "button", "--json"}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &resolvedAccessibilityFailureBackend{failure: errors.New("synthetic provider snapshot unavailable")}
			opens := 0
			stdout, stderr, exit := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, f), nil
				}
			})
			if exit == 0 || stdout != "" || !strings.Contains(stderr, f.failure.Error()) || opens != 1 || f.resolves != 1 || f.snapshots != tc.wantSnapshots || f.finds != tc.wantFinds {
				t.Fatalf("unavailable resolved tree/query args=%v exit=%d out=%q err=%q opens=%d resolves=%d snapshots=%d finds=%d", tc.args, exit, stdout, stderr, opens, f.resolves, f.snapshots, f.finds)
			}
		})
	}
}

type verifyingScrollAlignment struct {
	cliAutomationFake
	calls []accessibility.ScrollType
}

//nolint:unparam // Synthetic fixture preserves the interface/argument contract.
func (b *verifyingScrollAlignment) ScrollTo(_ context.Context, _ accessibility.NodeID, align accessibility.ScrollType) error {
	b.calls = append(b.calls, align)
	return nil
}

// Every documented alignment must retain its exact AT-SPI numeric meaning
// through local CLI decoding, session dispatch and the backend interface.
func TestAccessibilityRawCLIAlignmentMatrixDispatchesExactEnum(t *testing.T) {
	cases := []struct {
		name string
		want accessibility.ScrollType
	}{
		{"top-left", accessibility.ScrollTopLeft},
		{"bottom-right", accessibility.ScrollBottomRight},
		{"top-edge", accessibility.ScrollTopEdge},
		{"bottom-edge", accessibility.ScrollBottomEdge},
		{"left-edge", accessibility.ScrollLeftEdge},
		{"right-edge", accessibility.ScrollRightEdge},
		{"anywhere", accessibility.ScrollAnyWhere},
		{"any-where", accessibility.ScrollAnyWhere},
		{" RIGHT-EDGE ", accessibility.ScrollRightEdge},
	}
	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.name), func(t *testing.T) {
			backend := &verifyingScrollAlignment{}
			opens := 0
			args := []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/node", "--generation", "1", "--op", "scroll", "--alignment", tc.name}
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
				}
			})
			if code != 0 || stderr != "" || strings.TrimSpace(out) != "ok" {
				t.Fatalf("raw scroll %s code=%d out=%q stderr=%q", tc.name, code, out, stderr)
			}
			if opens != 1 || len(backend.calls) != 1 || backend.calls[0] != tc.want {
				t.Fatalf("alignment %q calls=%v opens=%d want %s", tc.name, backend.calls, opens, fmt.Sprint(tc.want))
			}
		})
	}
}

type semanticMutationRejectionProvider struct {
	cliAutomationFake
	failure    error
	focused    int
	textWrites int
}

func (b *semanticMutationRejectionProvider) GrabFocus(context.Context, accessibility.NodeID) error {
	b.focused++
	return b.failure
}
func (b *semanticMutationRejectionProvider) SetTextContents(context.Context, accessibility.NodeID, string) error {
	b.textWrites++
	return b.failure
}

// Having selected an accessible node does not prove that the mutation was
// accepted by its provider; never print success after a rejected operation.
func TestAccessibilitySemanticFocusAndTextPreserveMutationRejection(t *testing.T) {
	denied := errors.New("synthetic AT-SPI mutation rejected")
	for _, tc := range []struct {
		name                string
		args                []string
		wantFocus, wantText int
	}{
		{name: "focus refused", args: []string{"a11y", "focus", "--app", "Test", "--role", "button"}, wantFocus: 1},
		{name: "text refused", args: []string{"a11y", "text", "--app", "Test", "--role", "entry", "--value", "secret"}, wantText: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &semanticMutationRejectionProvider{failure: denied}
			opens := 0
			out, errOut, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
				}
			})
			if code == 0 || out != "" || !strings.Contains(errOut, denied.Error()) || opens != 1 || backend.focused != tc.wantFocus || backend.textWrites != tc.wantText {
				t.Fatalf("rejected mutation: code=%d out=%q err=%q opens=%d focus=%d text=%d", code, out, errOut, opens, backend.focused, backend.textWrites)
			}
		})
	}
}

type syntheticSemanticRootFailure struct {
	cliAutomationFake
	failure error
}

//nolint:unparam // This synthetic provider always denies scope resolution and intentionally returns no application.
func (s syntheticSemanticRootFailure) FindApplication(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	return accessibility.Application{}, s.failure
}

// Semantic mutations must fail before resolving or changing an element if
// application scope cannot be authenticated by the accessibility provider.
func TestAccessibilitySemanticMutationsRejectFailedScopeResolution(t *testing.T) {
	denied := errors.New("synthetic application scope unavailable")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "action", args: []string{"a11y", "action", "--app", "Test", "--role", "button"}},
		{name: "focus", args: []string{"a11y", "focus", "--app", "Test", "--role", "button"}},
		{name: "text", args: []string{"a11y", "text", "--app", "Test", "--role", "entry", "--value", "secret"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opens := 0
			stdout, stderr, code := captureRunIO(t, tc.args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, syntheticSemanticRootFailure{failure: denied}), nil
				}
			})
			if code == 0 || stdout != "" || !strings.Contains(stderr, denied.Error()) || opens != 1 {
				t.Fatalf("semantic mutation accepted invalid scope: code=%d stdout=%q stderr=%q opens=%d", code, stdout, stderr, opens)
			}
		})
	}
}

type cliWaitAccessibilityFake struct{ cliAutomationFake }

func (cliWaitAccessibilityFake) FindApplicationFresh(context.Context, accessibility.ApplicationFilter) (accessibility.Application, error) {
	return accessibility.Application{Node: accessibility.Node{
		ID: accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}, Name: "Test App", Role: "application",
	}, PID: 123}, nil
}

func (cliWaitAccessibilityFake) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	return make(chan accessibility.Event), nil
}

func TestAccessibilityWaitCLIRequiresTimeoutBeforeOpeningSession(t *testing.T) {
	opened := 0
	factory := func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			opened++
			return nil, nil
		}
	}
	for _, args := range [][]string{
		{"a11y", "wait", "--app", "Test", "--role", "button", "--name", "Save"},
		{"a11y", "wait", "--app", "Test", "--until", "made-up", "--timeout", "1s"},
		{"a11y", "wait", "--app", "Test", "--timeout", "1s", "--poll", "-1ms"},
		{"a11y", "wait", "--app", "Test", "--until", "hidden", "--timeout", "1s", "--visible-only"},
		{"a11y", "wait", "--app", "Test", "--until", "absent", "--timeout", "1s", "--visible-only"},
	} {
		_, stderr, code := captureRunIO(t, args, factory)
		if code == 0 || stderr == "" {
			t.Errorf("invalid wait arguments %v: code=%d stderr=%q", args, code, stderr)
		}
	}
	if opened != 0 {
		t.Fatalf("preflight failures opened %d desktop sessions", opened)
	}
}

func TestAccessibilityWaitCLIJSONUsesScopedFreshWait(t *testing.T) {
	open := func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliWaitAccessibilityFake{}), nil
	}
	stdout, stderr, code := captureRunIO(t,
		[]string{"a11y", "wait", "--app", "Test", "--pid", "123", "--role", "button", "--name", "Save", "--until", "present", "--timeout", "1s", "--json"},
		func(*cliConfig) sessionOpener { return open },
	)
	if code != 0 || stderr != "" {
		t.Fatalf("wait command code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	var result accessibilityWaitCLIResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("wait JSON %q: %v", stdout, err)
	}
	if !result.Satisfied || result.State != perfuncted.AccessibilityWaitPresent || result.Node == nil || result.Node.Name != "Save" || result.Evidence.Evaluations != 1 {
		t.Fatalf("wait JSON result = %+v", result)
	}
}

func TestAccessibilityWaitCommandAppearsInHelp(t *testing.T) {
	root := newRootCmd(func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return nil, nil }
	})
	command := findCommandPath(root, "a11y", "wait")
	if command == nil {
		t.Fatal("missing pf a11y wait")
	}
	if !strings.Contains(command.Long, "fresh complete snapshots") || command.Flags().Lookup("timeout") == nil || command.Flags().Lookup("until") == nil {
		t.Fatalf("wait command contract incomplete: long=%q flags=%v", command.Long, command.Flags().FlagUsages())
	}
}
