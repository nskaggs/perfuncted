//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/window"
)

type accessibilityCertificationApp struct {
	name     string
	launch   []string
	winMatch string
	extraEnv []string
}

// TestAccessibilityCertification is the strict cross-toolkit acceptance
// lane. It runs under headless Sway Wayland and certifies GTK/Qt AT-SPI
// behavior, not the GNOME Shell extension. The general integration suite keeps
// accessibility optional; every missing capability or failed semantic step
// in this target is fatal.
func TestAccessibilityCertification(t *testing.T) {
	s := mustSuite(t)
	if s.mode != displayHeadlessWayland {
		t.Fatalf("accessibility certification requires headless Wayland, got %s", s.mode)
	}
	if !s.pf.Has(perfuncted.CapabilityWindows) {
		t.Fatal("managed window capability is required for accessibility certification")
	}
	if strings.TrimSpace(s.rt.Get("DBUS_SESSION_BUS_ADDRESS")) == "" {
		t.Fatal("managed session did not publish a session D-Bus address")
	}
	if strings.TrimSpace(s.rt.Get("ATSPI_BUS_ADDRESS")) == "" {
		t.Fatal("managed session did not publish its AT-SPI bus address to child applications")
	}
	if s.rt.Has("AT_SPI_BUS") {
		t.Fatal("managed session unexpectedly published the X-root AT_SPI_BUS property variable")
	}
	status := s.pf.Capability(perfuncted.CapabilityAccessibility)
	if !status.Requested || !status.Required || !status.Available {
		t.Fatalf("AT-SPI capability is mandatory for certification: %+v", status)
	}
	for _, operation := range []string{"applications", "snapshot", "find", "window-root", "grab-focus", "set-text-contents", "insert-text", "set-caret", "invoke-action-by-name"} {
		if !status.Supports(operation) {
			t.Fatalf("AT-SPI certification requires operation %q: %+v", operation, status)
		}
	}

	apps := []accessibilityCertificationApp{
		{
			name:     "kwrite",
			launch:   []string{"kwrite"},
			winMatch: "kwrite",
			// Qt6 Wayland exposes AT-SPI only with both variables. Do not
			// remove one to "simplify": certification regresses to no bus.
			extraEnv: []string{"QT_ACCESSIBILITY=1", "QT_LINUX_ACCESSIBILITY_ALWAYS_ON=1"},
		},
		{
			name:     "gnome-text-editor",
			launch:   []string{"gnome-text-editor"},
			winMatch: "Text Editor",
			extraEnv: nil,
		},
	}
	for _, app := range apps {
		app := app
		t.Run(app.name, func(t *testing.T) {
			certifyAccessibilityEditor(t, s, app)
		})
	}
	t.Run("gtk-text-entry-parity", func(t *testing.T) {
		certifyGTKTextEntryParity(t, s)
	})
	t.Run("kwrite-save-action-parity", func(t *testing.T) {
		certifyKWriteSaveActionParity(t, s)
	})
}

func certifyGTKTextEntryParity(t *testing.T, s *suite) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// The Qt provider emits trailing non-text bytes for multibyte InsertText in
	// this headless session, so exact Unicode parity uses GTK; KWrite remains the
	// target for named Save-action parity below.
	textEditor := appSpec{name: "gnome-text-editor", launch: []string{"gnome-text-editor"}}
	semanticFile := filepath.Join(t.TempDir(), "semantic-parity.txt")
	semanticApp, semanticCmd := startEditorForParity(t, s, ctx, textEditor, semanticFile)
	_, _, _, editable := resolveEditorForParity(t, s, ctx, semanticApp.winMatch)

	marker := `AT-SPI parity: "quoted" \ path /`
	suffix := " — café 東京"
	expected := marker + suffix
	if err := s.pf.Accessibility.ReplaceEditableText(ctx, editable.ID, marker); err != nil {
		t.Fatalf("set semantic parity text: %v", err)
	}
	if err := s.pf.Accessibility.InsertText(ctx, editable.ID, int32(len([]rune(marker))), suffix); err != nil {
		t.Fatalf("append Unicode semantic parity suffix: %v", err)
	}
	if err := activateWindow(s.pf, ctx, semanticApp.winMatch); err != nil {
		t.Fatalf("activate semantic editor for physical save: %v", err)
	}
	if err := s.pf.Input.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("save semantic text through physical input: %v", err)
	}
	semanticContents := requireExactSavedFile(t, ctx, semanticFile, expected+"\n")
	stopEditorForParity(t, s, ctx, semanticApp.winMatch, semanticCmd)

	// The physical process shares the certification session to avoid starting a
	// second managed headless compositor. Its path uses only managed windows
	// and physical keyboard input; it makes no AT-SPI calls.
	// This proves operation-path independence, not startup without accessibility.
	physicalFile := filepath.Join(t.TempDir(), "physical-parity.txt")
	physicalApp, physicalCmd := startEditorForParity(t, s, ctx, textEditor, physicalFile)
	waitForEditorStable(t, s, ctx, physicalApp.winMatch)
	if err := s.pf.Input.Type(ctx, expected); err != nil {
		t.Fatalf("enter Unicode parity text through physical keyboard input: %v", err)
	}
	if err := s.pf.Input.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("save physical parity text: %v", err)
	}
	physicalContents := requireExactSavedFile(t, ctx, physicalFile, expected+"\n")
	if semanticContents != physicalContents {
		t.Fatalf("semantic and physical text paths differ: semantic=%q physical=%q", semanticContents, physicalContents)
	}
	stopEditorForParity(t, s, ctx, physicalApp.winMatch, physicalCmd)
}

func certifyKWriteSaveActionParity(t *testing.T, s *suite) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	kwrite := appSpec{
		name:     "kwrite",
		launch:   []string{"kwrite"},
		extraEnv: []string{"QT_ACCESSIBILITY=1", "QT_LINUX_ACCESSIBILITY_ALWAYS_ON=1"},
	}
	semanticFile := filepath.Join(t.TempDir(), "semantic-save-action.txt")
	semanticApp, semanticCmd := startEditorForParity(t, s, ctx, kwrite, semanticFile)
	_, scope, _, editable := resolveEditorForParity(t, s, ctx, semanticApp.winMatch)

	const expected = "Save action parity: café 東京"
	if err := s.pf.Accessibility.ReplaceEditableText(ctx, editable.ID, expected); err != nil {
		t.Fatalf("dirty semantic save-action document: %v", err)
	}
	dirtySnapshot, err := s.pf.Accessibility.Snapshot(ctx, scope.Root, certificationSnapshotOptions())
	if err != nil {
		t.Fatalf("snapshot dirty semantic save-action document: %v", err)
	}
	actionNode, action, err := findSaveAction(dirtySnapshot)
	if err != nil {
		t.Fatalf("find machine-readable Save action: %v", err)
	}
	selected, err := s.pf.Accessibility.InvokeActionByName(ctx, actionNode.ID, action.Name)
	if err != nil {
		t.Fatalf("invoke machine-readable Save action %q: %v", action.Name, err)
	}
	if selected.Name != action.Name {
		t.Fatalf("invoked action name = %q, want machine-readable name %q", selected.Name, action.Name)
	}
	semanticContents := requireExactSavedFile(t, ctx, semanticFile, expected+"\n")
	stopEditorForParity(t, s, ctx, semanticApp.winMatch, semanticCmd)

	physicalFile := filepath.Join(t.TempDir(), "physical-save-action.txt")
	physicalApp, physicalCmd := startEditorForParity(t, s, ctx, kwrite, physicalFile)
	waitForEditorStable(t, s, ctx, physicalApp.winMatch)
	if err := s.pf.Paste(ctx, expected); err != nil {
		t.Fatalf("dirty physical save-action document: %v", err)
	}
	if err := s.pf.Input.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("save physical save-action document: %v", err)
	}
	physicalContents := requireExactSavedFile(t, ctx, physicalFile, expected+"\n")
	if semanticContents != physicalContents {
		t.Fatalf("semantic action and physical save paths differ: semantic=%q physical=%q", semanticContents, physicalContents)
	}
	stopEditorForParity(t, s, ctx, physicalApp.winMatch, physicalCmd)
}

func startEditorForParity(t *testing.T, s *suite, ctx context.Context, app appSpec, saveFile string) (appSpec, *exec.Cmd) {
	t.Helper()
	// This setup observes and activates windows only. Physical-path callers do
	// not resolve a scope or invoke the Accessibility bundle.
	if err := os.WriteFile(saveFile, nil, 0o600); err != nil {
		t.Fatalf("create parity file: %v", err)
	}
	app.winMatch = filepath.Base(saveFile)
	app.saveFile = saveFile
	cmd, err := launchApp(s.rt, app, app.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch %s parity editor: %v", app.name, err)
	}
	t.Cleanup(func() { terminateCmd(cmd, 10*time.Second) })
	_, err = waitForWindow(s.pf, app.winMatch, 60*time.Second)
	if err != nil {
		t.Fatalf("find %s parity window %q: %v", app.name, app.winMatch, err)
	}
	if err := activateWindow(s.pf, ctx, app.winMatch); err != nil {
		t.Fatalf("activate %s parity window %q: %v", app.name, app.winMatch, err)
	}
	if err := waitForActiveWindow(ctx, s.pf, app.winMatch); err != nil {
		t.Fatalf("wait for active %s parity window %q: %v", app.name, app.winMatch, err)
	}
	return app, cmd
}

func resolveEditorForParity(
	t *testing.T,
	s *suite,
	ctx context.Context,
	windowMatch string,
) (window.Info, accessibility.WindowScope, accessibility.Snapshot, accessibility.Node) {
	t.Helper()
	info, err := findWindowInfo(s.pf, ctx, windowMatch)
	if err != nil {
		t.Fatalf("read managed KWrite parity window: %v", err)
	}
	target := accessibility.WindowTarget{
		ID:      info.NativeID,
		Title:   info.Title,
		PID:     info.PID,
		AppID:   info.AppID,
		Bounds:  accessibility.Rect{X: info.X, Y: info.Y, Width: info.W, Height: info.H},
		Active:  info.Active,
		Focused: info.Active,
	}
	scope, snapshot, err := resolveCertificationScopeAndSnapshot(ctx, s.pf.Accessibility, target, certificationSnapshotOptions())
	if err != nil {
		t.Fatalf("correlate parity window to AT-SPI: %v", err)
	}
	editable, err := findUniqueEditableTarget(snapshot)
	if err != nil {
		t.Fatalf("find unique parity editable node: %v", err)
	}
	if !editable.Focused {
		if err := s.pf.Accessibility.FocusNode(ctx, editable.ID); err != nil {
			t.Fatalf("focus parity editable node: %v", err)
		}
		if err := waitForFocusedAccessibilityNode(ctx, s.pf.Accessibility, target, editable.ID, certificationSnapshotOptions()); err != nil {
			t.Fatalf("verify parity editable focus: %v", err)
		}
	}
	return info, scope, snapshot, editable
}

func waitForActiveWindow(ctx context.Context, pf *perfuncted.Session, pattern string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastTitle string
	for {
		title, err := pf.Windows.ActiveTitle(ctx)
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "no focused window") {
			return err
		}
		if err == nil {
			lastTitle = title
			if strings.Contains(strings.ToLower(title), strings.ToLower(pattern)) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("active title remained %q, want to contain %q: %w", lastTitle, pattern, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForEditorStable(t *testing.T, s *suite, ctx context.Context, windowMatch string) {
	t.Helper()
	info, err := findWindowInfo(s.pf, ctx, windowMatch)
	if err != nil {
		t.Fatalf("find parity editor window %q: %v", windowMatch, err)
	}
	region := image.Rect(
		info.X+info.W/4,
		info.Y+info.H/4,
		info.X+3*info.W/4,
		info.Y+3*info.H/4,
	)
	stableCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := find.WaitForNoChange(stableCtx, s.pf.Screen, region, 3, 200*time.Millisecond, nil); err != nil {
		t.Fatalf("wait for parity editor window %q to settle: %v", windowMatch, err)
	}
}

func requireExactSavedFile(t *testing.T, ctx context.Context, path, expected string) string {
	t.Helper()
	contents, err := waitForFileContains(ctx, path, expected, 30*time.Second)
	if err != nil {
		current, readErr := os.ReadFile(path)
		t.Fatalf("wait for saved file %q: %v (current=%q readErr=%v)", path, err, current, readErr)
	}
	if contents != expected {
		t.Fatalf("saved file %q = %q, want exact content %q", path, contents, expected)
	}
	return contents
}

func stopEditorForParity(t *testing.T, s *suite, ctx context.Context, windowMatch string, cmd *exec.Cmd) {
	t.Helper()
	if err := closeWindow(s.pf, ctx, windowMatch); err != nil {
		t.Errorf("close parity editor window %q: %v", windowMatch, err)
	}
	terminateCmd(cmd, 5*time.Second)
	if err := waitForWindowClose(s.pf, windowMatch, 15*time.Second); err != nil {
		t.Errorf("wait for parity editor window %q to close: %v", windowMatch, err)
	}
}

func certifyAccessibilityEditor(t *testing.T, s *suite, representative accessibilityCertificationApp) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	saveFile := filepath.Join(t.TempDir(), representative.name+"-certification.txt")
	if err := os.WriteFile(saveFile, nil, 0o600); err != nil {
		t.Fatalf("create certification file: %v", err)
	}
	app := appSpec{
		name:     representative.name,
		launch:   representative.launch,
		winMatch: representative.winMatch,
		saveFile: saveFile,
		extraEnv: representative.extraEnv,
	}
	cmd, err := launchApp(s.rt, app, app.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch %s: %v", representative.name, err)
	}
	t.Cleanup(func() { terminateCmd(cmd, 10*time.Second) })

	if _, err := waitForWindow(s.pf, app.winMatch, 60*time.Second); err != nil {
		t.Fatalf("find managed %s window: %v", representative.name, err)
	}
	childEnv := readProcessEnvironment(t, cmd.Process.Pid)
	if got := strings.TrimSpace(childEnv["ATSPI_BUS_ADDRESS"]); got == "" {
		t.Fatalf("%s child did not inherit the managed ATSPI_BUS_ADDRESS", representative.name)
	} else if got != strings.TrimSpace(s.rt.Get("ATSPI_BUS_ADDRESS")) {
		t.Fatalf("%s child ATSPI bus = %q, managed session bus = %q", representative.name, got, s.rt.Get("ATSPI_BUS_ADDRESS"))
	}
	for _, key := range []string{"AT_SPI_BUS", "AT_SPI_BUS_ADDRESS"} {
		if _, ok := childEnv[key]; ok {
			t.Fatalf("%s child inherited host AT-SPI routing variable %s", representative.name, key)
		}
	}
	info, err := findWindowInfo(s.pf, ctx, app.winMatch)
	if err != nil {
		t.Fatalf("read managed %s window: %v", representative.name, err)
	}
	if strings.TrimSpace(info.NativeID) == "" {
		t.Fatalf("managed %s window has no canonical native identity: %+v", representative.name, info)
	}
	if err := activateWindow(s.pf, ctx, app.winMatch); err != nil {
		t.Fatalf("activate managed %s window before AT-SPI focus: %v", representative.name, err)
	}
	options := certificationSnapshotOptions()

	target := accessibility.WindowTarget{
		ID:      info.NativeID,
		Title:   info.Title,
		PID:     info.PID,
		AppID:   info.AppID,
		Bounds:  accessibility.Rect{X: info.X, Y: info.Y, Width: info.W, Height: info.H},
		Active:  info.Active,
		Focused: info.Active,
	}
	// The managed window metadata is already authoritative. Reusing it avoids
	// a second compositor discovery request while the AT-SPI provider is starting.
	correlationCtx, correlationCancel := context.WithTimeout(ctx, 60*time.Second)
	scope, snapshot, err := resolveCertificationScopeAndSnapshot(correlationCtx, s.pf.Accessibility, target, options)
	correlationCancel()
	if err != nil {
		logAccessibilityCorrelationDiagnostics(t, ctx, s.pf.Accessibility, info, optionsForCorrelationDiagnostics())
		t.Fatalf("correlate managed %s window to AT-SPI: %v", representative.name, err)
	}
	if !certificationNodeIDValid(scope.Root) || !certificationNodeIDValid(scope.ApplicationRoot) || scope.Generation == 0 {
		t.Fatalf("invalid correlated %s AT-SPI scope: %+v", representative.name, scope)
	}
	if info.PID != 0 && scope.PID != 0 && info.PID != scope.PID {
		t.Fatalf("%s compositor/AT-SPI PID mismatch: compositor=%d atspi=%d", representative.name, info.PID, scope.PID)
	}

	if snapshot.Root.ID != scope.Root || snapshot.Generation != scope.Generation || len(snapshot.Nodes) == 0 {
		t.Fatalf("invalid bounded %s AT-SPI tree: scope=%+v snapshot=%+v", representative.name, scope, snapshot)
	}
	editable, err := findUniqueEditableTarget(snapshot)
	if err != nil {
		t.Fatalf("unique editable %s semantic target: %v", representative.name, err)
	}
	if editable.ID.Generation != scope.Generation {
		t.Fatalf("%s editable target generation %d differs from scope generation %d", representative.name, editable.ID.Generation, scope.Generation)
	}

	if editable.Focused {
		t.Logf("AT-SPI editable target is already focused in the bounded snapshot")
	} else {
		if err := s.pf.Accessibility.FocusNode(ctx, editable.ID); err != nil {
			t.Fatalf("AT-SPI focus %s editable target: %v (node=%+v)", representative.name, err, editable)
		}
		if err := waitForFocusedAccessibilityNode(ctx, s.pf.Accessibility, target, editable.ID, options); err != nil {
			t.Fatalf("independent AT-SPI focus verification for %s: %v", representative.name, err)
		}
	}
	if representative.name == "kwrite" {
		actionNode, action, err := findSaveAction(snapshot)
		if err != nil {
			t.Fatalf("machine-readable named action target for %s: %v", representative.name, err)
		}
		selected, err := s.pf.Accessibility.InvokeActionByName(ctx, actionNode.ID, action.Name)
		if err != nil {
			t.Fatalf("invoke %s action %q by machine-readable name: %v", representative.name, action.Name, err)
		}
		if selected.Name != action.Name || selected.LocalizedName != action.LocalizedName {
			t.Fatalf("named action result = %+v, snapshot action = %+v", selected, action)
		}
	}

	marker := "Perfuncted AT-SPI certification " + representative.name
	if err := s.pf.Accessibility.ReplaceEditableText(ctx, editable.ID, marker); err != nil {
		t.Fatalf("AT-SPI text mutation %s: %v", representative.name, err)
	}
	unicodeSuffix := " — café"
	if err := s.pf.Accessibility.InsertText(ctx, editable.ID, int32(len([]rune(marker))), unicodeSuffix); err != nil {
		t.Fatalf("AT-SPI UTF-8 InsertText %s: %v", representative.name, err)
	}
	if err := s.pf.Accessibility.SetCaretOffset(ctx, editable.ID, int32(len([]rune(marker+unicodeSuffix)))); err != nil {
		t.Fatalf("AT-SPI caret mutation %s: %v", representative.name, err)
	}
	expected := marker + unicodeSuffix

	if err := activateWindow(s.pf, ctx, app.winMatch); err != nil {
		t.Fatalf("activate %s for save: %v", representative.name, err)
	}
	if err := s.pf.Input.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("save %s: %v", representative.name, err)
	}
	saved, err := waitForFileContains(ctx, saveFile, expected, 30*time.Second)
	if err != nil {
		t.Fatalf("independent on-disk verification for %s: %v", representative.name, err)
	}
	if !strings.Contains(saved, expected) {
		t.Fatalf("on-disk %s contents do not contain the Unicode certification text: %q", representative.name, saved)
	}

	if err := closeWindow(s.pf, ctx, app.winMatch); err != nil {
		t.Fatalf("close certified %s window: %v", representative.name, err)
	}
	if err := waitForWindowClose(s.pf, app.winMatch, 30*time.Second); err != nil {
		t.Fatalf("wait for certified %s window close: %v", representative.name, err)
	}
}

func findSaveAction(snapshot accessibility.Snapshot) (accessibility.Node, accessibility.Action, error) {
	for _, node := range snapshot.Nodes {
		role := strings.ToLower(node.Role)
		name := strings.ToLower(node.Name)
		if (!strings.Contains(role, "button") && !strings.Contains(role, "menu item")) || !strings.Contains(name, "save") || !node.Enabled || (!node.Visible && !node.Showing) {
			continue
		}
		for _, action := range node.Actions {
			if strings.TrimSpace(action.Name) != "" {
				return node, action, nil
			}
		}
	}
	return accessibility.Node{}, accessibility.Action{}, fmt.Errorf("no visible enabled Save button or menu item with a machine-readable action (nodes=%s)", strings.Join(snapshotNodeSummaries(snapshot.Nodes, 24), "; "))
}

func readProcessEnvironment(t *testing.T, pid int) map[string]string {
	t.Helper()
	if pid <= 0 {
		t.Fatalf("invalid launched application PID %d", pid)
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		t.Fatalf("read launched application environment for PID %d: %v", pid, err)
	}
	env := make(map[string]string)
	for _, entry := range strings.Split(string(raw), "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			env[key] = value
		}
	}
	return env
}

func findUniqueEditableTarget(snapshot accessibility.Snapshot) (accessibility.Node, error) {
	capabilityCandidates := make([]accessibility.Node, 0)
	editableCandidates := make([]accessibility.Node, 0)
	for _, node := range snapshot.Nodes {
		// Capability is the discriminator. Generic text-role nodes are common
		// in editor chrome, while EditableText identifies the mutation target.
		if !hasEditableTextInterface(node) {
			continue
		}
		capabilityCandidates = append(capabilityCandidates, node)
		// The provider can expose hidden editor internals with EditableText as
		// well. Only a currently visible/showing target is actionable in this
		// managed-window certification; FocusNode and mutation remain the final
		// provider-enforced checks.
		if node.Visible || node.Showing {
			editableCandidates = append(editableCandidates, node)
		}
	}
	switch len(editableCandidates) {
	case 0:
		return accessibility.Node{}, fmt.Errorf(
			"%w: no usable editable semantic target (EditableText nodes=%d, usable=%d, snapshot nodes=%d, truncated=%t, reasons=%v, nodes=%s)",
			accessibility.ErrNotFound,
			len(capabilityCandidates),
			len(editableCandidates),
			len(snapshot.Nodes),
			snapshot.Truncated,
			snapshot.TruncationReasons,
			strings.Join(snapshotNodeSummaries(snapshot.Nodes, 24), "; "),
		)
	case 1:
		return editableCandidates[0], nil
	default:
		labels := make([]string, 0, len(editableCandidates))
		for _, node := range editableCandidates {
			labels = append(labels, fmt.Sprintf("%s:%s(%s states=%v visible=%t showing=%t enabled=%t bounds=%+v parent=%s)", node.Role, node.Name, node.ID.ObjectPath, node.States, node.Visible, node.Showing, node.Enabled, node.Bounds, node.Parent.ObjectPath))
		}
		return accessibility.Node{}, fmt.Errorf(
			"%w: multiple editable semantic targets (%s)",
			accessibility.ErrAmbiguous,
			strings.Join(labels, ", "),
		)
	}
}

func snapshotNodeSummaries(nodes []accessibility.Node, limit int) []string {
	if limit <= 0 || limit > 64 {
		limit = 24
	}
	if len(nodes) < limit {
		limit = len(nodes)
	}
	summaries := make([]string, 0, limit)
	for _, node := range nodes[:limit] {
		summaries = append(summaries, fmt.Sprintf("%s:%q interfaces=%v states=%v visible=%t showing=%t enabled=%t", node.Role, node.Name, node.Interfaces, node.States, node.Visible, node.Showing, node.Enabled))
	}
	return summaries
}

func hasEditableTextInterface(node accessibility.Node) bool {
	for _, iface := range node.Interfaces {
		if strings.EqualFold(strings.TrimSpace(iface), "org.a11y.atspi.EditableText") {
			return true
		}
	}
	return false
}

func TestFindUniqueEditableTargetFiltersByCapabilityAndActionableState(t *testing.T) {
	generation := uint64(7)
	makeID := func(path string) accessibility.NodeID {
		return accessibility.NodeID{BusName: "org.test.Editor", ObjectPath: path, Generation: generation}
	}
	snapshot := accessibility.Snapshot{
		Root: accessibility.Node{ID: makeID("/window")},
		Nodes: []accessibility.Node{
			{
				ID:         makeID("/window/label"),
				Role:       "text",
				Name:       "Editor chrome",
				Interfaces: []string{"org.a11y.atspi.Text"},
				Visible:    true,
				Showing:    true,
			},
			{
				ID:         makeID("/window/internal"),
				Role:       "text",
				Name:       "Internal editor text",
				Interfaces: []string{"org.a11y.atspi.EditableText"},
			},
			{
				ID:         makeID("/window/document"),
				Role:       "text",
				Name:       "Document",
				Interfaces: []string{"org.a11y.atspi.EditableText"},
				Visible:    true,
				Showing:    true,
			},
		},
		Generation: generation,
	}

	got, err := findUniqueEditableTarget(snapshot)
	if err != nil {
		t.Fatalf("find unique editable target: %v", err)
	}
	if got.ID != makeID("/window/document") {
		t.Fatalf("selected node = %+v, want visible EditableText document", got)
	}
}

func TestFindUniqueEditableTargetRejectsMultipleActionableEditableNodes(t *testing.T) {
	generation := uint64(9)
	makeNode := func(path, name string) accessibility.Node {
		return accessibility.Node{
			ID:         accessibility.NodeID{BusName: "org.test.Editor", ObjectPath: path, Generation: generation},
			Role:       "text",
			Name:       name,
			Interfaces: []string{"org.a11y.atspi.EditableText"},
			Visible:    true,
			Showing:    true,
		}
	}

	_, err := findUniqueEditableTarget(accessibility.Snapshot{
		Nodes:      []accessibility.Node{makeNode("/window/one", "One"), makeNode("/window/two", "Two")},
		Generation: generation,
	})
	if !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("error = %v, want ErrAmbiguous for multiple actionable EditableText nodes", err)
	}
}

func certificationNodeIDValid(id accessibility.NodeID) bool {
	return strings.TrimSpace(id.BusName) != "" && strings.TrimSpace(id.ObjectPath) != "" && id.Generation > 0
}

func optionsForCorrelationDiagnostics() accessibility.SnapshotOptions {
	return accessibility.SnapshotOptions{MaxDepth: 4, MaxNodes: 256, MaxTextBytes: 256, MaxTotalBytes: 64 * 1024}
}

func certificationSnapshotOptions() accessibility.SnapshotOptions {
	return accessibility.SnapshotOptions{MaxDepth: 32, MaxNodes: 4096, MaxTextBytes: 4096, MaxTotalBytes: 512 * 1024, VisibleOnly: true}
}

func waitForAccessibilityWindow(
	ctx context.Context,
	bundle *perfuncted.AccessibilityBundle,
	target accessibility.WindowTarget,
) (accessibility.WindowScope, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		scope, err := bundle.AccessibilityWindow(ctx, target)
		if err == nil {
			return scope, nil
		}
		lastErr = err
		if !errors.Is(err, accessibility.ErrNotFound) && !errors.Is(err, accessibility.ErrStaleNode) && !errors.Is(err, accessibility.ErrStaleGeneration) {
			return accessibility.WindowScope{}, err
		}
		select {
		case <-ctx.Done():
			if lastErr != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return accessibility.WindowScope{}, ctx.Err()
			}
			return accessibility.WindowScope{}, lastErr
		case <-ticker.C:
		}
	}
}

func resolveCertificationScopeAndSnapshot(
	ctx context.Context,
	bundle *perfuncted.AccessibilityBundle,
	target accessibility.WindowTarget,
	options accessibility.SnapshotOptions,
) (accessibility.WindowScope, accessibility.Snapshot, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		scope, err := waitForAccessibilityWindow(ctx, bundle, target)
		if err != nil {
			return accessibility.WindowScope{}, accessibility.Snapshot{}, err
		}
		snapshot, err := bundle.Snapshot(ctx, scope.Root, options)
		if err == nil {
			return scope, snapshot, nil
		}
		if !errors.Is(err, accessibility.ErrStaleNode) && !errors.Is(err, accessibility.ErrStaleGeneration) {
			return accessibility.WindowScope{}, accessibility.Snapshot{}, err
		}
		select {
		case <-ctx.Done():
			return accessibility.WindowScope{}, accessibility.Snapshot{}, err
		case <-ticker.C:
		}
	}
}

func logAccessibilityCorrelationDiagnostics(
	t *testing.T,
	ctx context.Context,
	bundle *perfuncted.AccessibilityBundle,
	info window.Info,
	options accessibility.SnapshotOptions,
) {
	t.Helper()
	apps, err := bundle.Applications(ctx)
	if err != nil {
		t.Logf("AT-SPI correlation diagnostics: managed=%+v applications error=%v", info, err)
		return
	}
	t.Logf("AT-SPI correlation diagnostics: managed=%+v applications=%d", info, len(apps))
	for _, app := range apps {
		snapshot, snapshotErr := bundle.Snapshot(ctx, app.ID, options)
		if snapshotErr != nil {
			t.Logf("AT-SPI application name=%q pid=%d id=%+v snapshot error=%v", app.Name, app.PID, app.ID, snapshotErr)
			continue
		}
		for _, node := range snapshot.Nodes {
			t.Logf("AT-SPI application name=%q pid=%d node role=%q name=%q id=%+v bounds=%+v states=%v", app.Name, app.PID, node.Role, node.Name, node.ID, node.Bounds, node.States)
		}
	}
}

func waitForFocusedAccessibilityNode(
	ctx context.Context,
	bundle *perfuncted.AccessibilityBundle,
	target accessibility.WindowTarget,
	want accessibility.NodeID,
	options accessibility.SnapshotOptions,
) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, snapshot, err := resolveCertificationScopeAndSnapshot(ctx, bundle, target, options)
		if err != nil {
			return err
		}
		for _, node := range snapshot.Nodes {
			if node.ID.BusName == want.BusName && node.ID.ObjectPath == want.ObjectPath && node.Focused {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
