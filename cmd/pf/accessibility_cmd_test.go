package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

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
func (cliAccessibilityFake) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	rootID := accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 1}
	return accessibility.Snapshot{Root: accessibility.Node{ID: rootID, Name: "root", Role: "application"}, Nodes: []accessibility.Node{{ID: rootID, Name: "root", Role: "application"}}, Generation: 1, Source: "fake"}, nil
}
func (cliAccessibilityFake) Find(context.Context, accessibility.NodeID, accessibility.Query, accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	return []accessibility.Node{{Name: "Save", Role: "button"}}, nil
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

func (cliAutomationFake) SupportedOperations() []string {
	return []string{"applications", "snapshot", "find", "find-application", "focused", "at-point", "events", "invoke-action", "invoke-action-by-name", "invoke-default-action", "grab-focus", "scroll", "scroll-to-point", "set-position", "set-size", "set-extents", "set-current-value", "set-value", "set-text-contents", "replace-text", "insert-text", "delete-text", "copy-text", "cut-text", "paste-text", "set-caret", "set-text-selection", "add-text-selection", "remove-text-selection", "set-document-text-selections", "select-child", "deselect-child", "select-all", "clear-selection", "deselect-all", "deselect-selected-child", "select-row", "deselect-row", "select-column", "deselect-column", "window-root", "reopen"}
}

func (cliAutomationFake) InvokeAction(context.Context, accessibility.NodeID, int32) error {
	return nil
}
func (cliAutomationFake) InvokeActionByName(context.Context, accessibility.NodeID, string) error {
	return nil
}
func (cliAutomationFake) InvokeDefaultAction(context.Context, accessibility.NodeID) (accessibility.Action, error) { //nolint:unparam // the CLI fixture intentionally models a successful action.
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
func (cliAutomationFake) SetCurrentValue(context.Context, accessibility.NodeID, float64) error {
	return nil
}
func (cliAutomationFake) SetValue(context.Context, accessibility.NodeID, float64) error { return nil }
func (cliAutomationFake) SetTextContents(context.Context, accessibility.NodeID, string) error {
	return nil
}
func (cliAutomationFake) ReplaceText(context.Context, accessibility.NodeID, int32, int32, string) error {
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
func (cliAutomationFake) DeselectAll(context.Context, accessibility.NodeID) error    { return nil }
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

func (cliAmbiguousFake) Find(context.Context, accessibility.NodeID, accessibility.Query, accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	return []accessibility.Node{{Name: "one", Role: "button"}, {Name: "two", Role: "button"}}, nil
}

func TestAccessibilityCLIApplicationsJSON(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "apps", "--json"}, func(*cliConfig) sessionOpener {
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
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "apps"}, openCLIWithAccessibility)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Test App\tpid=123") || strings.HasPrefix(stdout, "{") || strings.HasPrefix(stdout, "[") {
		t.Fatalf("human apps output = %q", stdout)
	}
}

func TestAccessibilityCLIWorkflowCommandsUseSemanticScopeAndHelpers(t *testing.T) {
	for _, args := range [][]string{
		{"accessibility", "action", "--app", "Test", "--role", "button", "--json"},
		{"accessibility", "focus", "--app", "Test", "--role", "button"},
		{"accessibility", "text", "--app", "Test", "--role", "entry", "--value", "updated"},
	} {
		stdout, stderr, code := captureRunIO(t, args, openCLIWithAutomation)
		if code != 0 || stderr != "" {
			t.Fatalf("args=%v code=%d stderr=%q stdout=%q", args, code, stderr, stdout)
		}
	}
}

func TestAccessibilityCLIWindowOnlyScopeUsesCanonicalResolver(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "tree", "--window-id", "managed-window"}, openCLIWithWindowAndAccessibility)
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
}

func TestAccessibilityCLISemanticActionRejectsAmbiguity(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "action", "--app", "Test", "--role", "button"}, openCLIWithAmbiguousAccessibility)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "ambiguous") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAccessibilityCLIHelpKeepsProtocolCommandsUnderRaw(t *testing.T) {
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "--help"}, openCLIWithAccessibility)
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
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "raw", "action", "--bus", "org.test", "--path", "/node"}, openCLIWithAutomation)
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

func openCLIWithAutomation(*cliConfig) sessionOpener {
	return func(context.Context) (*perfuncted.Session, error) {
		return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, cliAutomationFake{}), nil
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
		{name: "tree", args: []string{"accessibility", "tree", "--app", "Test", "--json", "--max-depth", "2"}, want: "root"},
		{name: "find", args: []string{"accessibility", "find", "--app", "Test", "--json", "--role", "button", "--name", "Save"}, want: "Save"},
		{name: "focused", args: []string{"accessibility", "focused", "--json"}, want: "focused"},
		{name: "at point", args: []string{"accessibility", "at-point", "--json", "--x", "10", "--y", "20"}, want: "point"},
		{name: "at point positional", args: []string{"accessibility", "at-point", "--json", "10", "20"}, want: "point"},
		{name: "events", args: []string{"accessibility", "events", "--json"}, want: ""},
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
	stdout, stderr, code := captureRunIO(t, []string{"accessibility", "tree"}, openCLIWithAccessibility)
	if code == 0 || stdout != "" || !strings.Contains(stderr, "explicit --app") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAccessibilityCLIAutomationCommands(t *testing.T) {
	tests := [][]string{
		{"accessibility", "raw", "action", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"accessibility", "raw", "focus", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"accessibility", "raw", "scroll", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"accessibility", "raw", "set-value", "--bus", "org.test", "--path", "/node", "--generation", "1", "--value", "0.5"},
		{"accessibility", "raw", "set-text-contents", "--bus", "org.test", "--path", "/node", "--generation", "1", "--text", "updated"},
		{"accessibility", "raw", "set-text-selection", "--bus", "org.test", "--path", "/node", "--generation", "1", "--selection", "0", "--start", "0", "--end", "1"},
		{"accessibility", "raw", "add-text-selection", "--bus", "org.test", "--path", "/node", "--generation", "1", "--start", "0", "--end", "1"},
		{"accessibility", "raw", "remove-text-selection", "--bus", "org.test", "--path", "/node", "--generation", "1", "--selection", "0"},
		{"accessibility", "raw", "select-child", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"accessibility", "raw", "select-all", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"accessibility", "raw", "clear-selection", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"accessibility", "raw", "deselect-all", "--bus", "org.test", "--path", "/node", "--generation", "1"},
		{"accessibility", "raw", "select-row", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"accessibility", "raw", "deselect-row", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"accessibility", "raw", "select-column", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"accessibility", "raw", "deselect-column", "--bus", "org.test", "--path", "/node", "--generation", "1", "--index", "0"},
		{"accessibility", "raw", "reopen"},
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

func TestParseAccessibilityAttributes(t *testing.T) {
	got := parseAccessibilityAttributes([]string{"kind=Primary", " aria-label = Save = now", "invalid", "=empty"})
	if got["kind"] != "Primary" || got["aria-label"] != "Save = now" {
		t.Fatalf("attributes = %#v", got)
	}
	if _, ok := got["invalid"]; ok {
		t.Fatal("invalid attribute was accepted")
	}
}
