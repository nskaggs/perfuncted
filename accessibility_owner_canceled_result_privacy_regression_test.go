package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

// A canceled accessibility query must not return sensitive content alongside
// context.Canceled, even when a provider ignores cancellation and completes
// after the caller abandoned authorization to receive those results.
func TestAccessibilityOwnerCanceledFindDoesNotReturnLateNodes(t *testing.T) {
	found := accessibility.Node{Name: "private document title", ID: accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/private"}}
	provider := &ownerQueryBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(1, "find"), findNodes: []accessibility.Node{found}, ignoreCancel: true, findEntered: make(chan struct{})}
	gate := make(chan struct{})
	provider.findRelease = gate
	session := NewSessionForTesting(nil, nil, nil, nil, nil, provider)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		nodes []accessibility.Node
		err   error
	}
	output := make(chan result, 1)
	go func() {
		nodes, err := owner.Find(ctx, accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{})
		output <- result{nodes, err}
	}()
	<-provider.findEntered
	cancel()
	close(gate)
	select {
	case got := <-output:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("late node query error=%v", got.err)
		}
		if len(got.nodes) != 0 {
			t.Fatalf("canceled query leaked stale private nodes: %+v", got.nodes)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled provider did not return")
	}
}
