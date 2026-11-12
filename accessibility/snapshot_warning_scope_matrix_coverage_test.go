package accessibility

import "testing"

// An AT-SPI property read failure must invalidate a selector that depends on
// that property, but an unrelated optional interface failure must not make
// an otherwise authoritative name or role match ambiguous.
func TestSnapshotSemanticWarningSelectorDependencyMatrix(t *testing.T) {
	cases := []struct {
		name, warning  string
		selector       Selector
		affects, known bool
	}{
		{"name selector requires name", accessibleIface + ".Name: missing", Selector{Name: "Editor"}, true, true},
		{"ancestor name requires name", accessibleIface + ".Name: missing", Selector{Ancestors: []AncestorSelector{{Name: "Dialog"}}}, true, true},
		{"unrelated name failure", accessibleIface + ".Name: missing", Selector{Role: "button"}, false, true},
		{"role selector requires role", accessibleIface + ".GetRole: missing", Selector{Role: "button"}, true, true},
		{"ancestor role requires role", accessibleIface + ".GetRole: missing", Selector{Ancestors: []AncestorSelector{{Role: "dialog"}}}, true, true},
		{"unrelated role failure", accessibleIface + ".GetRole: missing", Selector{Name: "Editor"}, false, true},
		{"state selector", accessibleIface + ".GetState: missing", Selector{States: []string{"focused"}}, true, true},
		{"unrelated state", accessibleIface + ".GetState: missing", Selector{Name: "Editor"}, false, true},
		{"child count always relevant", accessibleIface + ".ChildCount: missing", Selector{Name: "Editor"}, true, true},
		{"interfaces required by text", accessibleIface + ".GetInterfaces: missing", Selector{Text: "hello"}, true, true},
		{"interfaces unrelated to role", accessibleIface + ".GetInterfaces: missing", Selector{Role: "button"}, false, true},
		{"attribute selector", accessibleIface + ".GetAttributes: missing", Selector{Attributes: map[string]string{"type": "email"}}, true, true},
		{"unrelated attributes", accessibleIface + ".GetAttributes: missing", Selector{Name: "Editor"}, false, true},
		{"relations needed for label", accessibleIface + ".GetRelationSet: missing", Selector{Label: "Address"}, true, true},
		{"unrelated relations", accessibleIface + ".GetRelationSet: missing", Selector{Role: "button"}, false, true},
		{"text needed by label", textIface + ": missing", Selector{Label: "Address"}, true, true},
		{"unrelated text", textIface + ": missing", Selector{Role: "button"}, false, true},
		{"optional value failure", valueIface + ".GetCurrentValue: missing", Selector{Name: "Editor"}, false, true},
		{"optional action failure", actionIface + ".GetActions: missing", Selector{Role: "button"}, false, true},
		{"optional table failure", tableIface + ".GetRowCount: missing", Selector{Name: "Editor"}, false, true},
		{"unknown provider failure", "org.example.Provider.Failure: missing", Selector{Name: "Editor"}, true, false},
		{"warning missing separator", "unstructured failure", Selector{Name: "Editor"}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := semanticWarningAffectsSelector(tc.warning, tc.selector); got != tc.affects {
				t.Fatalf("warning %q selector %+v affects=%v want=%v", tc.warning, tc.selector, got, tc.affects)
			}
			if got := knownNodeWarning(tc.warning); got != tc.known {
				t.Fatalf("warning %q known=%v want=%v", tc.warning, got, tc.known)
			}
		})
	}
}
