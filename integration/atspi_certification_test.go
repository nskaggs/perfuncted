//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
	"github.com/nskaggs/perfuncted/window"
)

type accessibilityCertificationApp struct {
	name     string
	launch   []string
	winMatch string
	extraEnv []string
}

// TestAccessibilityCertification is the strict cross-toolkit acceptance
// lane. The general integration suite deliberately keeps accessibility
// optional; this test is run by its own headless-Wayland target and every
// missing capability or failed semantic step is fatal.
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
	for _, operation := range []string{"applications", "snapshot", "find", "window-root", "grab-focus", "set-text-contents"} {
		if !status.Supports(operation) {
			t.Fatalf("AT-SPI certification requires operation %q: %+v", operation, status)
		}
	}

	apps := []accessibilityCertificationApp{
		{
			name:     "kwrite",
			launch:   []string{"kwrite"},
			winMatch: "kwrite",
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

	marker := "Perfuncted AT-SPI certification " + representative.name
	if err := s.pf.Accessibility.ReplaceEditableText(ctx, editable.ID, marker); err != nil {
		t.Fatalf("AT-SPI text mutation %s: %v", representative.name, err)
	}

	if err := activateWindow(s.pf, ctx, app.winMatch); err != nil {
		t.Fatalf("activate %s for save: %v", representative.name, err)
	}
	if err := s.pf.Input.Type(ctx, "{ctrl+s}"); err != nil {
		t.Fatalf("save %s: %v", representative.name, err)
	}
	saved, err := waitForFileContains(ctx, saveFile, marker, 30*time.Second)
	if err != nil {
		t.Fatalf("independent on-disk verification for %s: %v", representative.name, err)
	}
	if !strings.Contains(saved, marker) {
		t.Fatalf("on-disk %s contents do not contain certification marker: %q", representative.name, saved)
	}

	if err := closeWindow(s.pf, ctx, app.winMatch); err != nil {
		t.Fatalf("close certified %s window: %v", representative.name, err)
	}
	if err := waitForWindowClose(s.pf, app.winMatch, 30*time.Second); err != nil {
		t.Fatalf("wait for certified %s window close: %v", representative.name, err)
	}
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
