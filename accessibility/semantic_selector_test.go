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
		{name: "tree warning", snapshot: Snapshot{ProviderErrors: 1, Warnings: []string{"/child: child disappeared"}}},
		{
			name:     "selector name warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".Name: name read failed"),
			selector: Selector{Name: "save"},
		},
		{
			name:     "selector state warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".GetState: state read failed"),
			selector: Selector{States: []string{"enabled"}},
		},
		{
			name:     "selector text warning",
			snapshot: snapshotWithNodeWarning(textIface + ": text read failed"),
			selector: Selector{Text: "save"},
		},
		{
			name:     "selector attributes warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".GetAttributes: attributes read failed"),
			selector: Selector{Attributes: map[string]string{"kind": "primary"}},
		},
		{
			name:     "label relation warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".GetRelationSet: relation read failed"),
			selector: Selector{Label: "email"},
		},
		{
			name:     "ancestor name warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".Name: name read failed"),
			selector: Selector{Ancestors: []AncestorSelector{{Name: "dialog"}}},
		},
		{
			name:     "child count warning",
			snapshot: snapshotWithNodeWarning(accessibleIface + ".ChildCount: child count read failed"),
			selector: Selector{Role: "button"},
		},
		{
			name:     "inconsistent diagnostics",
			snapshot: Snapshot{ProviderErrors: 2, Warnings: []string{"/child: child disappeared"}},
		},
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

func TestValidateSemanticSnapshotIgnoresWarningsOutsideSelectorEvidence(t *testing.T) {
	tests := []struct {
		name     string
		warning  string
		selector Selector
	}{
		{name: "optional value", warning: valueIface + ".MinimumValue: unsupported", selector: Selector{Role: "button"}},
		{name: "optional action", warning: actionIface + ".GetActions: unsupported", selector: Selector{Role: "button"}},
		{name: "optional selection", warning: selectionIface + ".GetSelectedChildCount: unsupported", selector: Selector{Role: "button"}},
		{name: "optional table", warning: tableIface + ".NRows: unsupported", selector: Selector{Role: "button"}},
		{name: "optional document", warning: documentIface + ": no readable document properties", selector: Selector{Role: "button"}},
		{name: "unused name", warning: accessibleIface + ".Name: unsupported", selector: Selector{Role: "button"}},
		{name: "unused state", warning: accessibleIface + ".GetState: unsupported", selector: Selector{Role: "button"}},
		{name: "unused attributes", warning: accessibleIface + ".GetAttributes: unsupported", selector: Selector{Role: "button"}},
		{name: "unused interfaces", warning: accessibleIface + ".GetInterfaces: unsupported", selector: Selector{Role: "button"}},
		{name: "text interface for role", warning: textIface + ": unsupported", selector: Selector{Role: "button"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateSemanticSnapshot(snapshotWithNodeWarning(test.warning), test.selector); err != nil {
				t.Fatalf("ValidateSemanticSnapshot = %v, want complete selector evidence", err)
			}
		})
	}
}

func snapshotWithNodeWarning(warning string) Snapshot {
	return Snapshot{
		Nodes:          []Node{{Role: "button", Warnings: []string{warning}}},
		ProviderErrors: 1,
		Warnings:       []string{warning},
	}
}

func TestValidateSemanticSnapshotAllowsTextTruncationWithoutTextSelector(t *testing.T) {
	if err := ValidateSemanticSnapshot(Snapshot{Nodes: []Node{{TextTruncated: true}}}, Selector{Role: "button"}); err != nil {
		t.Fatalf("ValidateSemanticSnapshot = %v, want complete role observation", err)
	}
}
