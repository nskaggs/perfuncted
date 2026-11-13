package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type manyMatchingAccessibilityBackend struct {
	*bundleAccessibilityFake
	root          accessibility.NodeID
	nodes         []accessibility.Node
	snapshotCalls int
}

func (b *manyMatchingAccessibilityBackend) Snapshot(_ context.Context, _ accessibility.NodeID, opts accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.snapshotCalls++
	if !opts.Fresh {
		return accessibility.Snapshot{}, errors.New("semantic selection must request a fresh snapshot")
	}
	return accessibility.Snapshot{Root: b.nodes[0], Nodes: b.nodes, Generation: b.root.Generation}, nil
}

// Candidate diagnostics are bounded to avoid a second provider traversal, but
// ambiguity must not disappear merely because more than 32 nodes match.
// All matching identities must remain distinguishable in the error evidence.
func TestAccessibilityFindOneSelectorRetainsLargeAmbiguityEvidence(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/application", Generation: 1}
	nodes := []accessibility.Node{{ID: root, Name: "Editor", Role: "application"}}
	for i := 0; i < 40; i++ {
		id := accessibility.NodeID{BusName: root.BusName, ObjectPath: fmt.Sprintf("/button%d", i), Generation: 1}
		nodes = append(nodes, accessibility.Node{ID: id, Parent: root, Name: "Save", Role: "button"})
	}
	backend := &manyMatchingAccessibilityBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, root: root, nodes: nodes}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	selector := accessibility.Selector{Name: "Save", Role: "button"}
	node, err := session.Accessibility.FindOneSelector(context.Background(), root, selector, accessibility.SnapshotOptions{})
	if !errors.Is(err, accessibility.ErrAmbiguous) || node.ID.BusName != "" || backend.snapshotCalls != 1 {
		t.Fatalf("ambiguity: node=%+v err=%v snapshots=%d", node, err, backend.snapshotCalls)
	}
	var matchErr *accessibility.MatchError
	if !errors.As(err, &matchErr) {
		t.Fatalf("missing candidate evidence: %v", err)
	}
	seen := map[accessibility.NodeID]bool{}
	for _, candidate := range matchErr.Candidates {
		if seen[candidate.ID] {
			t.Fatalf("duplicate candidate: %+v", candidate.ID)
		}
		seen[candidate.ID] = true
	}
	for _, target := range nodes[1:] {
		if !seen[target.ID] {
			t.Fatalf("ambiguity evidence missing matched identity: %+v", target.ID)
		}
	}
	if len(seen) != 40 {
		t.Fatalf("candidate evidence=%d, want 40", len(seen))
	}
}
