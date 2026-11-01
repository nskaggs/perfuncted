package perfuncted

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// Every accessibility query carries a caller context. An absent context
// must not be silently replaced with Background and disclose private nodes.
func TestAccessibilityOwnerQueriesRejectNilContextBeforeProviderDispatch(t *testing.T) {
	node := accessibility.Node{Name: "confidential desktop title", ID: accessibility.NodeID{BusName: "org.example.Private", ObjectPath: "/secret"}}
	base := newOwnerLifecycleBackend(3, "find", "focused", "at-point", "applications")
	base.apps = []accessibility.Application{{Name: "confidential program"}}
	backend := &ownerQueryBackend{ownerLifecycleBackend: base, findNodes: []accessibility.Node{node}, selected: node}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"Applications", func() error {
			r, e := owner.Applications(nil)
			if len(r) > 0 {
				t.Errorf("nil context disclosed applications: %+v", r)
			}
			return e
		}},
		{"Find", func() error {
			r, e := owner.Find(nil, accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{})
			if len(r) > 0 {
				t.Errorf("nil context disclosed nodes: %+v", r)
			}
			return e
		}},
		{"Focused", func() error {
			r, e := owner.Focused(nil, accessibility.SnapshotOptions{})
			if !reflect.DeepEqual(r, accessibility.Node{}) {
				t.Errorf("nil context disclosed focused node: %+v", r)
			}
			return e
		}},
		{"AtPoint", func() error {
			r, e := owner.AtPoint(nil, 1, 1)
			if !reflect.DeepEqual(r, accessibility.Node{}) {
				t.Errorf("nil context disclosed point node: %+v", r)
			}
			return e
		}},
		{"Snapshot", func() error {
			_, e := owner.Snapshot(nil, accessibility.NodeID{}, accessibility.SnapshotOptions{})
			return e
		}},
		{"FindApplication", func() error { _, e := owner.FindApplication(nil, accessibility.ApplicationFilter{}); return e }},
		{"FindApplicationFresh", func() error { _, e := owner.FindApplicationFresh(nil, accessibility.ApplicationFilter{}); return e }},
		{"ResolveWindow", func() error { _, e := owner.ResolveWindow(nil, accessibility.WindowTarget{}); return e }},
		{"ResolveWindowFresh", func() error { _, e := owner.ResolveWindowFresh(nil, accessibility.WindowTarget{}); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil || !strings.Contains(err.Error(), "nil context") {
				t.Fatalf("nil accessibility query context not rejected: %v", err)
			}
		})
	}
	if backend.findCalls != 0 || backend.focusCalls != 0 || backend.pointCalls != 0 {
		t.Fatalf("nil context reached private provider: %+v", backend)
	}
	// The new boundary must not reject normal caller contexts.
	if _, err := owner.Find(context.Background(), accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{}); err != nil {
		t.Fatal(err)
	}
}
