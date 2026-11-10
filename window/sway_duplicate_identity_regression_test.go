package window

import (
	"context"
	"strings"
	"testing"
)

// A Sway GET_TREE response must not advertise conflicting windows with the
// same stable identifier. A caller must not act on an ambiguous handle.
func TestSwayTreeRejectsDuplicateActionableWindowIdentities(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":42,"type":"con","name":"first"}],"floating_nodes":[{"id":42,"type":"floating_con","name":"conflicting"}]}`
	m, finish := newSyntheticSwayCommands(t, tree, `[{"success":true}]`)
	windows, err := m.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "duplicate") || len(windows) != 0 {
		t.Fatalf("duplicated window identity accepted: windows=%+v error=%v", windows, err)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}
