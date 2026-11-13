package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

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
