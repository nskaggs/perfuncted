package window

import (
	"context"
	"testing"
)

// Sway IPC window handles are positive. Zero is not a usable window identity,
// so a malformed leaf must never be advertised for handle-based actions.
func TestSwayTreeDiscoveryExcludesZeroAndNegativeWindowHandles(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":0,"type":"con","name":"zero-handle","rect":{"width":100,"height":100}},{"id":-1,"type":"con","name":"negative-handle","rect":{"width":100,"height":100}},{"id":42,"type":"con","name":"valid","rect":{"width":100,"height":100}}]}`
	manager, finish := newSyntheticSwayCommands(t, tree, `[{"success":true}]`)
	windows, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 1 || windows[0].StableID() != "42" || windows[0].Title != "valid" {
		t.Fatalf("unusable Sway handles advertised: %+v", windows)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}
