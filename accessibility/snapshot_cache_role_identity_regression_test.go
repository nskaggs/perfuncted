package accessibility

import "testing"

// Snapshot SkipRoles controls whether a subtree is explored. A cache entry
// captured under one set of roles must never be reused by a distinct set of
// skipped roles, even when names themselves contain separators.
func TestSnapshotKeyUniquelyEncodesSkipRoleBoundaries(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.App", ObjectPath: "/application", Generation: 11, Incarnation: 4}
	cases := []struct {
		name  string
		roles []string
	}{
		{"no skips", nil},
		{"single role", []string{"menu"}},
		{"two roles", []string{"menu", "item"}},
		{"comma in role", []string{"menu,item"}},
		{"three roles", []string{"menu", "item", "dialog"}},
		{"two composite roles", []string{"menu,item", "dialog"}},
		{"one composite role", []string{"menu,item,dialog"}},
		{"trailing empty role", []string{"menu", ""}},
		{"single role with comma", []string{"menu,"}},
		{"empty role", []string{""}},
		{"two empty roles", []string{"", ""}},
	}
	keys := map[string]string{}
	for _, tc := range cases {
		key := snapshotKey(root, SnapshotOptions{SkipRoles: tc.roles})
		if prev, ok := keys[key]; ok {
			t.Fatalf("cache key collision between %q and %q: a truncated snapshot could be returned for a different traversal", prev, tc.name)
		}
		keys[key] = tc.name
	}
}
