package window

import (
	"context"
	"strings"
	"testing"
)

// A compositor response cannot establish an authoritative active title if
// multiple distinct windows claim focus; choosing the first leaks a false
// focus observation to automation clients.
func TestSwayActiveTitleRejectsConflictingFocusedWindows(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":41,"type":"con","name":"first private window","focused":true},{"id":42,"type":"con","name":"second private window","focused":true}]}`
	manager, finish := newSyntheticSwayCommands(t, tree, `[{"success":true}]`)
	title, err := manager.ActiveTitle(context.Background())
	if title != "" || err == nil || !strings.Contains(err.Error(), "multiple focused") {
		t.Fatalf("ambiguous Sway focus incorrectly trusted: title=%q err=%v", title, err)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}
