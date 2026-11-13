package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type closingAccessibilityFindSnapshot struct {
	*bundleAccessibilityFake
	after func()
	calls int
	root  accessibility.NodeID
}

func (b *closingAccessibilityFindSnapshot) Snapshot(_ context.Context, _ accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	node := accessibility.Node{ID: b.root, Role: "application"}
	return accessibility.Snapshot{Root: node, Nodes: []accessibility.Node{node, {ID: accessibility.NodeID{BusName: b.root.BusName, ObjectPath: "/secret", Generation: b.root.Generation}, Parent: b.root, Role: "button", Name: "private-button"}}, Generation: b.root.Generation}, nil
}

// A completed backend traversal cannot expose private accessibility matches
// after session ownership was revoked, whether the caller requests all matches
// or a unique semantic selector.
func TestAccessibilityFindDiscardsResultsAfterSessionClosure(t *testing.T) {
	for _, name := range []string{"find", "find-one-selector"} {
		t.Run(name, func(t *testing.T) {
			root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 1}
			backend := &closingAccessibilityFindSnapshot{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, root: root}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var nameOut string
			var err error
			if name == "find" {
				var got []accessibility.Node
				got, err = session.Accessibility.Find(context.Background(), root, accessibility.Query{Name: "private-button", Role: "button"}, accessibility.SnapshotOptions{})
				if len(got) != 0 {
					nameOut = got[0].Name
				}
			} else {
				var got accessibility.Node
				got, err = session.Accessibility.FindOneSelector(context.Background(), root, accessibility.Selector{Name: "private-button", Role: "button"}, accessibility.SnapshotOptions{})
				nameOut = got.Name
			}
			if !errors.Is(err, ErrSessionClosed) || nameOut != "" || backend.calls != 1 {
				t.Fatalf("find %s after closure: node=%q err=%v backend calls=%d", name, nameOut, err, backend.calls)
			}
		})
	}
}
