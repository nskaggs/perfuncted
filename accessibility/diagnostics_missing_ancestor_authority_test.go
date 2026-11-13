package accessibility

import (
	"reflect"
	"testing"
)

// Diagnostic breadcrumbs must reflect only ancestors actually present in the
// same bounded snapshot. A vanished or incomplete parent chain must not be
// fabricated from a stale, out-of-snapshot accessibility identity.
func TestCandidateBreadcrumbStopsAtMissingParentWithoutFabricatingAncestors(t *testing.T) {
	app := NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 5}
	dialog := NodeID{BusName: app.BusName, ObjectPath: "/dialog", Generation: 5}
	absent := NodeID{BusName: app.BusName, ObjectPath: "/unobserved", Generation: 5}
	button := NodeID{BusName: app.BusName, ObjectPath: "/save", Generation: 5}
	for _, tc := range []struct {
		name  string
		nodes []Node
		want  []string
	}{
		{"direct parent missing", []Node{{ID: app, Name: "Editor", Role: "application"}, {ID: button, Name: "Save", Role: "button", Parent: absent}}, nil},
		{"known parent followed by missing ancestor", []Node{{ID: app, Name: "Editor", Role: "application"}, {ID: dialog, Name: "Settings", Role: "dialog", Parent: absent}, {ID: button, Name: "Save", Role: "button", Parent: dialog}}, []string{"dialog: Settings"}},
		{"complete chain", []Node{{ID: app, Name: "Editor", Role: "application"}, {ID: dialog, Name: "Settings", Role: "dialog", Parent: app}, {ID: button, Name: "Save", Role: "button", Parent: dialog}}, []string{"application: Editor", "dialog: Settings"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidates := CandidatesForQuery(Snapshot{Nodes: tc.nodes}, Query{Name: "Save"}, 32)
			if len(candidates) != 1 || candidates[0].ID != button || !reflect.DeepEqual(candidates[0].Breadcrumb, tc.want) {
				t.Fatalf("candidates=%+v want breadcrumbs=%v", candidates, tc.want)
			}
		})
	}
}
