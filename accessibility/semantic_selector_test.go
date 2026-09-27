package accessibility

import (
	"errors"
	"testing"
)

func TestFilterSnapshotSelectorResolvesLabelAndOrderedAncestors(t *testing.T) {
	root := NodeID{BusName: "org.test", ObjectPath: "/root", Generation: 4}
	form := NodeID{BusName: "org.test", ObjectPath: "/form", Generation: 4}
	label := NodeID{BusName: "org.test", ObjectPath: "/label", Generation: 4}
	field := NodeID{BusName: "org.test", ObjectPath: "/field", Generation: 4}
	snapshot := Snapshot{Root: Node{ID: root}, Nodes: []Node{
		{ID: root, Role: "application"},
		{ID: form, Parent: root, Role: "group", Name: "Account"},
		{ID: label, Parent: form, Role: "label", Name: "Email address", Relations: map[string][]NodeID{"label-for": {field}}},
		{ID: field, Parent: form, Role: "text input", States: []string{"enabled", "editable"}},
	}}
	got := FilterSnapshotSelector(snapshot, Selector{
		Role:   "input",
		Label:  "email",
		States: []string{"enabled", "editable"},
		Ancestors: []AncestorSelector{
			{Role: "group", Name: "account"},
			{Role: "application"},
		},
	})
	if len(got) != 1 || got[0].ID != field {
		t.Fatalf("selector matches = %+v, want field %v", got, field)
	}
}

func TestValidateSemanticSnapshotRejectsIncompleteTargetEvidence(t *testing.T) {
	tests := []struct {
		name     string
		snapshot Snapshot
		selector Selector
	}{
		{name: "truncated", snapshot: Snapshot{Truncated: true}},
		{name: "provider error", snapshot: Snapshot{ProviderErrors: 1}},
		{name: "snapshot warning", snapshot: Snapshot{Warnings: []string{"child disappeared"}}},
		{name: "node warning", snapshot: Snapshot{Nodes: []Node{{Warnings: []string{"name read failed"}}}}},
		{name: "text truncation", snapshot: Snapshot{Nodes: []Node{{TextTruncated: true}}}, selector: Selector{Text: "hidden suffix"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSemanticSnapshot(test.snapshot, test.selector); !errors.Is(err, ErrIncompleteSnapshot) {
				t.Fatalf("ValidateSemanticSnapshot error = %v, want ErrIncompleteSnapshot", err)
			}
		})
	}
}

func TestValidateSemanticSnapshotAllowsTextTruncationWithoutTextSelector(t *testing.T) {
	if err := ValidateSemanticSnapshot(Snapshot{Nodes: []Node{{TextTruncated: true}}}, Selector{Role: "button"}); err != nil {
		t.Fatalf("ValidateSemanticSnapshot = %v, want complete role observation", err)
	}
}
