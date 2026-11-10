package window

import (
	"context"
	"testing"
)

// Only an actionable leaf container with a positive Sway identity can own
// the reported active-window title. Container-group and zero-ID focus flags
// are not a real window even when they carry a nonempty name.
func TestSwayActiveTitleSkipsNonWindowFocusedNodes(t *testing.T) {
	cases := []struct{ name, tree string }{
		{"zero identity", `{"id":1,"type":"root","nodes":[{"id":0,"type":"con","name":"phantom focus","focused":true}]}`},
		{"group container", `{"id":1,"type":"root","nodes":[{"id":22,"type":"con","name":"group title","focused":true,"nodes":[{"id":23,"type":"con","name":"child","focused":false}]}]}`},
		{"negative identity", `{"id":1,"type":"root","nodes":[{"id":-3,"type":"con","name":"phantom focus","focused":true}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, tc.tree, `[{"success":true}]`)
			title, err := m.ActiveTitle(context.Background())
			if title != "" || err == nil {
				t.Errorf("non-window focus reported: title=%q err=%v", title, err)
			}
			if _, err := finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
