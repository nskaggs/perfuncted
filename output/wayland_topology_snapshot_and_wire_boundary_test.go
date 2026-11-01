package output

import (
	"context"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWaylandTopologyReconcileSkipsDuplicateAndNilEntriesWhilePreservingSnapshots(t *testing.T) {
	l := &WaylandLister{outputs: []*waylandOutput{
		{globalID: 7, info: Info{Name: "eDP-7", Backend: "wayland", Available: true}},
		nil,
		{globalID: 2, info: Info{Name: "DP-2", Backend: "wayland", Available: true}},
	}}
	prior := l.snapshotOutputs()
	if len(prior) != 2 || prior[0].Name != "DP-2" || prior[1].Name != "eDP-7" {
		t.Fatalf("initial outputs not sorted or nil entries leaked: %+v", prior)
	}
	prior[0].Name = "mutated outside snapshot"
	if got := l.snapshotOutputs(); got[0].Name != "DP-2" {
		t.Fatalf("caller modified shared output state: %+v", got)
	}
	globals := []wl.GlobalEvent{
		{Name: 2, Interface: "wl_output", Version: 4},
		{Name: 2, Interface: "wl_output", Version: 1},
		{Name: 10, Interface: "wl_output", Version: 3},
		{Name: 10, Interface: "wl_output", Version: 2},
		{Name: 99, Interface: "wl_seat", Version: 4},
	}
	desired, added, removed := l.topologyChanges(outputGlobals(globals))
	if len(desired) != 2 || len(added) != 1 || added[0].globalID != 10 || len(removed) != 1 || removed[0].globalID != 7 {
		t.Fatalf("topology failed to reconcile announcements desired=%v added=%+v removed=%+v", desired, added, removed)
	}
	if remain := l.snapshotOutputs(); len(remain) != 1 || remain[0].Name != "DP-2" {
		t.Fatalf("reconciliation lost retained output: %+v", remain)
	}
	if err := l.releaseOutput(context.Background(), removed[0]); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseOutput(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestWaylandWireStringReaderKeepsFirstValidFieldOnTruncatedSecond(t *testing.T) {
	first := wlStringData("Acme")
	second := appendUint32(nil, 6)
	second = append(second, []byte("bad")...)
	got, secondValue, ok := readWlStrings(append(first, second...), 0)
	if ok || got != "Acme" || secondValue != "" {
		t.Fatalf("truncated second wire string accepted: first=%q second=%q ok=%v", got, secondValue, ok)
	}
	if v, _, valid := readWlString([]byte{1, 2, 3}, 0); valid || v != "" {
		t.Fatalf("truncated header accepted %q", v)
	}
	for _, invalid := range [][]byte{
		append(appendUint32(nil, 3), []byte{'a', 'b', 'c'}...),
		append(appendUint32(nil, 5), []byte{'a', 0, 'b', 0, 0, 0, 0, 0}...),
		append(appendUint32(nil, 2), []byte{0xff, 0, 0, 0}...),
	} {
		if got, _, valid := readWlString(invalid, 0); valid {
			t.Fatalf("untrusted wire string accepted: %q from %v", got, invalid)
		}
	}
	if got, _, ok := readWlString(wlStringData("DP-1"), 0); !ok || !strings.EqualFold(got, "DP-1") {
		t.Fatalf("valid wire string rejected: %q ok=%v", got, ok)
	}
}
