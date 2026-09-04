package perfuncted

import (
	"context"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type waitAccessibilityBackend struct {
	calls int
}

func (b *waitAccessibilityBackend) SupportedOperations() []string { return []string{"find", "focused"} }
func (b *waitAccessibilityBackend) Applications(context.Context) ([]accessibility.Application, error) {
	return nil, nil
}
func (b *waitAccessibilityBackend) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	return accessibility.Snapshot{}, nil
}
func (b *waitAccessibilityBackend) Find(_ context.Context, _ accessibility.NodeID, query accessibility.Query, _ accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	b.calls++
	if b.calls < 2 {
		return nil, nil
	}
	node := accessibility.Node{Name: "Save", Text: "Saved", States: []string{"enabled", "focused"}, Attributes: map[string]string{"kind": "primary"}}
	if query.Text != "" && query.Text != node.Text {
		return nil, nil
	}
	return []accessibility.Node{node}, nil
}
func (b *waitAccessibilityBackend) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	return accessibility.Node{Focused: true}, nil
}
func (b *waitAccessibilityBackend) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	return accessibility.Node{}, accessibility.ErrNotFound
}
func (b *waitAccessibilityBackend) Close() error { return nil }

func TestAccessibilityWaitConditionsRefreshUntilSatisfied(t *testing.T) {
	backend := &waitAccessibilityBackend{}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	root := accessibility.NodeID{BusName: "org.test", ObjectPath: "/root"}
	if err := session.Wait(ctx, AccessibilityNodeExists(root, accessibility.Query{Role: "button"}, accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityNodeExists: %v", err)
	}
	if err := session.Wait(ctx, AccessibilityStateContains(root, accessibility.Query{Name: "Save"}, []string{"focused", "enabled"}, accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityStateContains: %v", err)
	}
	if err := session.Wait(ctx, AccessibilityTextContains(root, "Saved", accessibility.SnapshotOptions{}), WaitEvery(time.Millisecond)); err != nil {
		t.Fatalf("AccessibilityTextContains: %v", err)
	}
	if backend.calls < 3 {
		t.Fatalf("backend calls = %d, want repeated authoritative refreshes", backend.calls)
	}
	_ = session.Close()
}
