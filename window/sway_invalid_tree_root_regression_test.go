package window

import (
	"context"
	"strings"
	"testing"
)

// A syntactically valid JSON body is not necessarily a Sway GET_TREE result.
// Treating JSON null or an object with no root identity as an empty desktop
// causes automation to falsely report that windows have disappeared.
func TestSwayWindowDiscoveryRejectsInvalidTreeRoot(t *testing.T) {
	for _, tc := range []struct{ name, tree string }{
		{"null", "null"},
		{"empty_object", "{}"},
		{"zero_id", `{"id":0,"type":"root"}`},
		{"wrong_type", `{"id":1,"type":"con"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, tc.tree, `[{"success":true}]`)
			windows, err := m.List(context.Background())
			if err == nil || !strings.Contains(err.Error(), "invalid tree root") || len(windows) != 0 {
				t.Errorf("invalid Sway root accepted: windows=%+v err=%v", windows, err)
			}
			if _, err := finish(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSwayWindowDiscoveryAcceptsGenuineEmptyRoot(t *testing.T) {
	m, finish := newSyntheticSwayCommands(t, `{"id":1,"type":"root","nodes":[]}`, `[{"success":true}]`)
	windows, err := m.List(context.Background())
	if err != nil || len(windows) != 0 {
		t.Errorf("valid empty desktop windows=%+v err=%v", windows, err)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}
