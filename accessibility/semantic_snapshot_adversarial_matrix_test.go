package accessibility

import (
	"errors"
	"testing"
)

func TestFilterSnapshotAdversarialSearchDoesNotOvermatch(t *testing.T) {
	root := NodeID{BusName: "org.example.App", ObjectPath: "/app", Generation: 7}
	form := NodeID{BusName: root.BusName, ObjectPath: "/form", Generation: 7}
	field := NodeID{BusName: root.BusName, ObjectPath: "/email", Generation: 7}
	label := NodeID{BusName: root.BusName, ObjectPath: "/emailLabel", Generation: 7}
	secret := NodeID{BusName: root.BusName, ObjectPath: "/secret", Generation: 7}
	snap := Snapshot{Nodes: []Node{
		{ID: root, Role: "application", Name: "Mail"},
		{ID: form, Parent: root, Role: "group", Name: "New message"},
		{ID: label, Parent: form, Role: "label", Name: "Recipient address", Relations: map[string][]NodeID{"label-for": {field}}},
		{ID: field, Parent: form, Role: "text input", Name: "Recipient", Text: "recipient@example.com", States: []string{"editable", "enabled"}, Attributes: map[string]string{"autocomplete": "EMAIL"}},
		{ID: secret, Parent: form, Role: "text input", Name: "Password", Redacted: true, Text: "", States: []string{"protected"}},
	}}
	cases := []struct {
		name  string
		query Query
		count int
		first NodeID
	}{
		{"exact case-insensitive role", Query{Role: "APPLICATION"}, 1, root},
		{"name case-insensitive substring", Query{Name: "RECIP", Role: "text input"}, 1, field},
		{"text substring", Query{Text: "@EXAMPLE."}, 1, field},
		{"state and attribute", Query{States: []string{"EDITABLE", "enabled"}, Attributes: map[string]string{"AUTOComplete": "email"}}, 1, field},
		{"missing required state", Query{States: []string{"editable", "focused"}}, 0, NodeID{}},
		{"missing attribute", Query{Attributes: map[string]string{"autocomplete": "telephone"}}, 0, NodeID{}},
		{"redacted contents not searchable", Query{Text: "password secret"}, 0, NodeID{}},
		{"no implicit partial attribute matching", Query{Attributes: map[string]string{"autocomplete": "em"}}, 0, NodeID{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := FilterSnapshot(snap, tc.query)
			if len(found) != tc.count {
				t.Fatalf("query %+v matched %d nodes (%+v), want %d", tc.query, len(found), found, tc.count)
			}
			if tc.count == 1 && found[0].ID != tc.first {
				t.Fatalf("query %+v selected wrong target %v", tc.query, found[0].ID)
			}
		})
	}
	if got := FilterSnapshotSelector(snap, Selector{Role: "input", Label: "recipient", Ancestors: []AncestorSelector{{Role: "group", Name: "new message"}, {Role: "application", Name: "mail"}}}); len(got) != 1 || got[0].ID != field {
		t.Fatalf("label plus ordered ancestors resolved wrong target: %+v", got)
	}
	if got := FilterSnapshotSelector(snap, Selector{Role: "input", Label: "recipient", Ancestors: []AncestorSelector{{Role: "application"}, {Role: "group"}}}); len(got) != 0 {
		t.Fatalf("reversed ancestry unexpectedly matched: %+v", got)
	}
	if got := FilterSnapshotSelector(snap, Selector{Label: "not a label"}); len(got) != 0 {
		t.Fatalf("unknown label unexpectedly matched: %+v", got)
	}
}

func TestAccessibilityToolkitCacheEnforcesHandleEpochAndIsolation(t *testing.T) {
	b := &dbusBackend{generation: 11}
	node := NodeID{BusName: "org.example.App", ObjectPath: "/node", Generation: 11, Incarnation: 1}
	if got, ok, err := b.cachedToolkit(node); err != nil || ok || got != "" {
		t.Fatalf("uncached toolkit = %q, %t, %v", got, ok, err)
	}
	if err := b.cacheToolkit(node, "GTK"); err != nil {
		t.Fatalf("cacheToolkit: %v", err)
	}
	if got, ok, err := b.cachedToolkit(node); err != nil || !ok || got != "GTK" {
		t.Fatalf("cached toolkit = %q, %t, %v", got, ok, err)
	}
	alias := NodeID{BusName: "org.example.Other", ObjectPath: "/node", Generation: 11, Incarnation: 1}
	if got, ok, err := b.cachedToolkit(alias); err != nil || ok || got != "" {
		t.Fatalf("toolkit leaked across application buses: %q %t %v", got, ok, err)
	}
	b.Invalidate(node)
	if _, ok, err := b.cachedToolkit(node); !errors.Is(err, ErrStaleNode) || ok {
		t.Fatalf("stale generation reused toolkit after invalidation: %v", err)
	}
	if err := b.cacheToolkit(node, "stale"); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("stale generation accepted cache write: %v", err)
	}
	fresh := NodeID{BusName: node.BusName, ObjectPath: node.ObjectPath, Generation: 12, Incarnation: 1}
	if got, ok, err := b.cachedToolkit(fresh); err != nil || ok || got != "" {
		t.Fatalf("invalidation failed to clear toolkit: %q %t %v", got, ok, err)
	}
	if err := b.cacheToolkit(fresh, "Qt"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := b.cachedToolkit(fresh); err != nil || !ok || got != "Qt" {
		t.Fatalf("new generation toolkit not loaded: %q %t %v", got, ok, err)
	}
}

func TestAccessibilityParentCacheRejectsStaleAndForeignHandles(t *testing.T) {
	b := &dbusBackend{generation: 4}
	parent := NodeID{BusName: "org.example.App", ObjectPath: "/parent", Generation: 4}
	child := NodeID{BusName: "org.example.App", ObjectPath: "/parent/child", Generation: 4}
	b.recordObjectParent(child, parent)
	identity := objectIdentity{busName: child.BusName, objectPath: child.ObjectPath}
	if p, ok := b.parents[identity]; !ok || p.objectPath != parent.ObjectPath {
		t.Fatalf("parent relation not cached: %+v", b.parents)
	}
	b.recordObjectParent(NodeID{BusName: child.BusName, ObjectPath: "/invalid", Generation: 3}, parent)
	b.recordObjectParent(child, NodeID{})
	if len(b.parents) != 1 {
		t.Fatalf("stale/invalid node altered ancestry tracking: %+v", b.parents)
	}
	b.Invalidate(parent)
	if len(b.parents) != 0 {
		t.Fatalf("cached parents survived epoch change: %+v", b.parents)
	}
	b.recordObjectParent(child, parent)
	if len(b.parents) != 0 {
		t.Fatalf("stale parent handles restored old ancestry: %+v", b.parents)
	}
}
