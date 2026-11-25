//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

func certifyReleaseAccessibilityCLI(t *testing.T, s *suite) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "pf")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../cmd/pf")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, out)
	}
	doc := filepath.Join(t.TempDir(), "cli-semantic.txt")
	app, owner := startEditorForParity(t, s, ctx, appSpec{name: "kwrite", launch: []string{"kwrite"}, extraEnv: []string{"QT_ACCESSIBILITY=1", "QT_LINUX_ACCESSIBILITY_ALWAYS_ON=1"}}, doc)
	info, originalScope, _, editable := resolveEditorForParity(t, s, ctx, app.winMatch)
	run := func(args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pf %v: %v: %s", args, err, diagnostic.String())
		}
		return out
	}
	scope := []string{"--window-id", info.NativeID, "--json", "--allow-sensitive", "--max-depth", "20", "--max-nodes", "1000"}
	selector := append(append([]string{}, scope...), "--role", editable.Role, "--state", "focused")
	call := func(op string, flags []string, extra ...string) []byte {
		args := append([]string{"a11y", op}, flags...)
		return run(append(args, extra...)...)
	}
	var apps []accessibility.Application
	if err := json.Unmarshal(run("a11y", "apps", "--json"), &apps); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range apps {
		if a.ID.BusName == editable.ID.BusName {
			found = true
		}
	}
	if !found {
		t.Fatal("CLI applications omitted the live editor")
	}
	var tree accessibility.Snapshot
	if err := json.Unmarshal(call("tree", scope), &tree); err != nil || len(tree.Nodes) == 0 {
		t.Fatalf("CLI tree: %v", err)
	}
	found = false
	for _, n := range tree.Nodes {
		if n.ID.BusName == editable.ID.BusName && n.ID.ObjectPath == editable.ID.ObjectPath {
			found = true
		}
	}
	if !found {
		t.Fatal("CLI scoped tree omitted live editable node")
	}
	var matches []accessibility.Node
	if err := json.Unmarshal(call("find", selector), &matches); err != nil || len(matches) != 1 || matches[0].ID.ObjectPath != editable.ID.ObjectPath {
		t.Fatalf("CLI find: %+v, %v", matches, err)
	}
	var focused accessibility.Node
	if err := json.Unmarshal(run("a11y", "focused", "--json"), &focused); err != nil || !focused.Focused || focused.ID.BusName != editable.ID.BusName {
		t.Fatalf("CLI focused: %+v %v", focused, err)
	}
	var waited struct {
		Satisfied bool
		Node      accessibility.Node
	}
	if err := json.Unmarshal(call("wait", selector, "--until", "focused", "--timeout", "5s"), &waited); err != nil || !waited.Satisfied || waited.Node.ID.ObjectPath != editable.ID.ObjectPath {
		t.Fatalf("CLI wait: %+v %v", waited, err)
	}
	const marker = "CLI semantic save: café 東京"
	call("text", selector, "--value", marker)
	// Read-only fresh observation; never replay the text mutation.
	options := certificationSnapshotOptions()
	options.AllowSensitive = true
	locator := s.pf.Accessibility.LocatorForWindow(info.NativeID, accessibility.Selector{Role: editable.Role, Name: editable.Name}, options)
	if _, err := resolveCertificationEditable(ctx, locator, editable.ID, marker); err != nil {
		t.Fatalf("CLI text did not reach exact original node: %v", err)
	}
	// A separate CLI connection must validate its generation and handle.
	run("a11y", "raw", "--op", "set-caret", "--bus", matches[0].ID.BusName, "--path", matches[0].ID.ObjectPath, "--generation", strconv.FormatUint(matches[0].ID.Generation, 10), "--offset", strconv.Itoa(len([]rune(marker))), "--json")
	dirty, err := retryChangedSnapshot(ctx, func() (accessibility.Snapshot, error) {
		return s.pf.Accessibility.Snapshot(ctx, originalScope.Root, certificationSnapshotOptions())
	})
	if err != nil {
		t.Fatal(err)
	}
	save, action, err := findSaveAction(dirty)
	if err != nil {
		t.Fatal(err)
	}
	// Qt can expose multiple enabled controls with the same substring. A
	// semantic action must reject ambiguity rather than select arbitrarily.
	actionSelector := append(append([]string{}, scope...), "--name", save.Name, "--role", save.Role, "--state", "enabled", "--visible-only")
	var candidates []accessibility.Node
	if err := json.Unmarshal(call("find", actionSelector), &candidates); err != nil || len(candidates) == 0 {
		t.Fatalf("live Save candidates: %+v %v", candidates, err)
	}
	if len(candidates) == 1 {
		call("action", actionSelector, "--action", action.Name)
	} else {
		args := append([]string{"a11y", "action"}, actionSelector...)
		args = append(args, "--action", action.Name)
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		out, err := cmd.Output()
		if err == nil || len(out) != 0 || !strings.Contains(strings.ToLower(diagnostic.String()), "ambiguous") {
			t.Fatalf("ambiguous live action: stdout=%q stderr=%q err=%v", out, diagnostic.String(), err)
		}
		if data, err := os.ReadFile(doc); err != nil || len(data) != 0 {
			t.Fatalf("ambiguous action unexpectedly saved: %q %v", data, err)
		}
		run("a11y", "raw", "--op", "action", "--bus", save.ID.BusName, "--path", save.ID.ObjectPath, "--generation", strconv.FormatUint(save.ID.Generation, 10), "--action-name", action.Name, "--json")
	}
	requireExactSavedFile(t, ctx, doc, marker+"\n")
	// Focus is a real request and the wait confirms the original node.
	// Some Qt versions reject GrabFocus on an already focused editor. A
	// rejection must not claim success; a successful request must still be
	// verified by the independent scoped wait.
	focusArgs := append([]string{"a11y", "focus"}, selector...)
	focusCmd := exec.CommandContext(ctx, binary, focusArgs...)
	focusCmd.Env = s.rt.EnvList()
	var focusDiagnostic bytes.Buffer
	focusCmd.Stderr = &focusDiagnostic
	focusOut, focusErr := focusCmd.Output()
	if focusErr != nil && (len(focusOut) != 0 || !strings.Contains(focusDiagnostic.String(), "mutation rejected")) {
		t.Fatalf("live focus: stdout=%q stderr=%q err=%v", focusOut, focusDiagnostic.String(), focusErr)
	}
	if focusErr == nil {
		var focusNode accessibility.Node
		if err := json.Unmarshal(focusOut, &focusNode); err != nil || focusNode.ID.ObjectPath != editable.ID.ObjectPath {
			t.Fatalf("focus receipt: %+v %v", focusNode, err)
		}
	}
	if err := json.Unmarshal(call("wait", selector, "--until", "focused", "--timeout", "5s"), &waited); err != nil || !waited.Satisfied || waited.Node.ID.ObjectPath != editable.ID.ObjectPath {
		t.Fatalf("focus state: %+v %v", waited, err)
	}
	if err := json.Unmarshal(run("a11y", "focused", "--json"), &focused); err != nil || !focused.Focused || focused.ID.BusName != editable.ID.BusName {
		t.Fatalf("CLI focus: %+v %v", focused, err)
	}
	stopEditorForParity(t, s, ctx, app.winMatch, owner)
	t.Log("CLI semantic discovery, focus, text, raw caret and action verified on disk")
}
