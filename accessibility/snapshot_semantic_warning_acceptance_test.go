package accessibility

import (
	"errors"
	"strings"
	"testing"
)

// A named/role/label/ancestor selector must never claim that a target is
// absent when the AT-SPI fields required to find it were unreadable. Optional
// metadata failures, however, must not block unrelated semantic discovery.
func TestSnapshotWarningCompletenessAcrossSelectorDependencies(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.App", ObjectPath: "/app", Generation: 8}
	input := NodeID{BusName: root.BusName, ObjectPath: "/app/field", Generation: 8}
	for _, tc := range []struct {
		name, warning string
		selector      Selector
		complete      bool
	}{
		{"name-required", accessibleIface + ".Name: synthetic unavailable", Selector{Name: "Confirm"}, false},
		{"name-label-required", accessibleIface + ".Name: synthetic unavailable", Selector{Label: "Account"}, false},
		{"name-ancestor-required", accessibleIface + ".Name: synthetic unavailable", Selector{Ancestors: []AncestorSelector{{Name: "Dialog"}}}, false},
		{"name-not-required", accessibleIface + ".Name: synthetic unavailable", Selector{Role: "entry"}, true},
		{"role-required", accessibleIface + ".GetRole: synthetic unavailable", Selector{Role: "entry"}, false},
		{"role-ancestor-required", accessibleIface + ".GetRole: synthetic unavailable", Selector{Ancestors: []AncestorSelector{{Role: "dialog"}}}, false},
		{"role-not-required", accessibleIface + ".GetRole: synthetic unavailable", Selector{Name: "Confirm"}, true},
		{"states-required", accessibleIface + ".GetState: synthetic unavailable", Selector{States: []string{"focused"}}, false},
		{"states-not-required", accessibleIface + ".GetState: synthetic unavailable", Selector{Role: "entry"}, true},
		{"attributes-required", accessibleIface + ".GetAttributes: synthetic unavailable", Selector{Attributes: map[string]string{"data-id": "42"}}, false},
		{"attributes-not-required", accessibleIface + ".GetAttributes: synthetic unavailable", Selector{Role: "entry"}, true},
		{"label-relation-required", accessibleIface + ".GetRelationSet: synthetic unavailable", Selector{Label: "Account"}, false},
		{"label-relation-not-required", accessibleIface + ".GetRelationSet: synthetic unavailable", Selector{Role: "entry"}, true},
		{"text-required", textIface + ": synthetic unavailable", Selector{Text: "Confirm"}, false},
		{"text-label-required", textIface + ": synthetic unavailable", Selector{Label: "Account"}, false},
		{"text-not-required", textIface + ": synthetic unavailable", Selector{Role: "entry"}, true},
		{"interface-required-for-text", accessibleIface + ".GetInterfaces: synthetic unavailable", Selector{Text: "Confirm"}, false},
		{"interface-irrelevant", accessibleIface + ".GetInterfaces: synthetic unavailable", Selector{Role: "entry"}, true},
		{"child-count-always-required", accessibleIface + ".ChildCount: synthetic unavailable", Selector{Role: "entry"}, false},
		{"action-optional", actionIface + ".GetActions: synthetic unavailable", Selector{Role: "entry"}, true},
		{"value-optional", valueIface + ".MinimumValue: synthetic unavailable", Selector{Role: "entry"}, true},
		{"selection-optional", selectionIface + ".GetSelectedChildCount: synthetic unavailable", Selector{Role: "entry"}, true},
		{"table-optional", tableIface + ".NRows: synthetic unavailable", Selector{Role: "entry"}, true},
		{"document-optional", documentIface + ".PageCount: synthetic unavailable", Selector{Role: "entry"}, true},
		{"unclassified-provider-warning", "org.synthetic.UnknownMethod: synthetic unavailable", Selector{Role: "entry"}, false},
		{"malformed-unknown-warning", "provider returned nonsense", Selector{Role: "entry"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{Root: Node{ID: root, Role: "application"}, Nodes: []Node{{ID: root, Role: "application"}, {ID: input, Parent: root, Role: "entry", Warnings: []string{tc.warning}}}, Warnings: []string{tc.warning}, ProviderErrors: 1}
			err := ValidateSemanticSnapshot(snapshot, tc.selector)
			if tc.complete && err != nil {
				t.Fatalf("optional failure %q incorrectly blocks independent selection: %v", tc.warning, err)
			}
			if !tc.complete && !errors.Is(err, ErrIncompleteSnapshot) {
				t.Fatalf("required failure %q not reported as incomplete: %v", tc.warning, err)
			}
		})
	}
}
func TestSnapshotGlobalTraversalWarningsCannotBeMistakenForBenignNodeMetadata(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.App", ObjectPath: "/root", Generation: 6}
	for _, tc := range []struct {
		name                         string
		nodeWarnings, globalWarnings []string
		providerErrors               int
		wantIncomplete               bool
	}{
		{"matching optional warning", []string{actionIface + ".GetActions: error"}, []string{actionIface + ".GetActions: error"}, 1, false},
		{"node warning not collected", []string{actionIface + ".GetActions: error"}, nil, 0, true},
		{"unknown global warning", nil, []string{"child traversal failed"}, 1, true},
		{"provider count mismatch", nil, []string{actionIface + ".GetActions: error"}, 2, true},
		{"extra unrelated optional warning", nil, []string{actionIface + ".GetActions: error"}, 1, false},
		{"truncated without reasons", nil, nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{Root: Node{ID: id}, Nodes: []Node{{ID: id, Warnings: tc.nodeWarnings}}, Warnings: tc.globalWarnings, ProviderErrors: tc.providerErrors}
			if strings.HasPrefix(tc.name, "truncated") {
				snapshot.Truncated = true
			}
			err := ValidateSemanticSnapshot(snapshot, Selector{Role: "entry"})
			if tc.wantIncomplete && !errors.Is(err, ErrIncompleteSnapshot) {
				t.Fatalf("traversal incomplete but reported verified: %v", err)
			}
			if !tc.wantIncomplete && err != nil {
				t.Fatalf("benign optional warning blocked selection: %v", err)
			}
		})
	}
}
