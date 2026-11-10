package window

import (
	"context"
	"testing"
)

// Compositor geometry is advisory, but public widths and heights cannot be
// negative: unknown or malformed dimensions must be reported as zero rather
// than leaking an impossible rectangle to geometry-sensitive callers.
func TestSwayWindowDiscoveryClampsNegativeDimensionsToUnknown(t *testing.T) {
	tree := `{"id":1,"type":"root","nodes":[{"id":41,"type":"con","name":"invalid width","rect":{"x":-10,"y":40,"width":-20,"height":300}},{"id":42,"type":"floating_con","name":"invalid height","rect":{"x":5,"y":-20,"width":100,"height":-1}},{"id":43,"type":"con","name":"valid","rect":{"x":10,"y":20,"width":640,"height":480}}]}`
	manager, finish := newSyntheticSwayCommands(t, tree, `[{"success":true}]`)
	windows, err := manager.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 3 {
		t.Fatalf("expected all 3 identities despite malformed geometry, got %+v", windows)
	}
	for _, tc := range []struct {
		id   string
		w, h int
	}{{"41", 0, 300}, {"42", 100, 0}, {"43", 640, 480}} {
		matched := false
		for _, win := range windows {
			if win.StableID() != tc.id {
				continue
			}
			matched = true
			if win.W != tc.w || win.H != tc.h {
				t.Errorf("window %s exported invalid geometry %dx%d, expected %dx%d", tc.id, win.W, win.H, tc.w, tc.h)
			}
		}
		if !matched {
			t.Errorf("window %s omitted", tc.id)
		}
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}
