package output

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// XDG-output is optional, and outputs can disappear between discovery and
// binding. No protocol operation should be attempted for an absent manager,
// absent output, or an output whose wl_output proxy was already removed.
func TestWaylandOptionalXDGBindingSkipsAbsentTopology(t *testing.T) {
	output := &waylandOutput{info: Info{Name: "survivor", Backend: "wayland"}}
	manager := &wl.RawProxy{}
	for _, tc := range []struct {
		name   string
		lister *WaylandLister
		output *waylandOutput
	}{
		{name: "manager absent", lister: &WaylandLister{}, output: output},
		{name: "output absent", lister: &WaylandLister{xdgManager: manager}},
		{name: "output proxy absent", lister: &WaylandLister{xdgManager: manager}, output: output},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.lister.bindXDGOutput(context.Background(), tc.output); err != nil {
				t.Fatalf("missing optional XDG topology should be skipped: %v", err)
			}
			if tc.output != nil && tc.output.xdgProxy != nil {
				t.Fatal("missing optional XDG topology registered a proxy")
			}
		})
	}
}
