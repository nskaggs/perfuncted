package accessibility

import "testing"

func TestCandidatesForQueryStateOrAttributeOnlyStillReportsContext(t *testing.T) {
	id := NodeID{BusName: "org.test", ObjectPath: "/button", Generation: 4}
	snapshot := Snapshot{Nodes: []Node{{ID: id, Role: "button", Name: "Save"}}}
	queries := []struct {
		name  string
		query Query
	}{
		{name: "states", query: Query{States: []string{"focused"}}},
		{name: "attributes", query: Query{Attributes: map[string]string{"aria-label": "save"}}},
	}
	for _, tc := range queries {
		t.Run(tc.name, func(t *testing.T) {
			candidates := CandidatesForQuery(snapshot, tc.query, 1)
			if len(candidates) != 1 || candidates[0].ID != id {
				t.Fatalf("candidates = %+v, want button diagnostic", candidates)
			}
		})
	}
}
