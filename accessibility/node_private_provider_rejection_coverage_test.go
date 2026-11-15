package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Missing observation context must be rejected before even looking up a
// provider object, and disconnected providers must not publish node content.
func TestNodeReadsRejectNilContextAndDisconnectedProvider(t *testing.T) {
	b := &dbusBackend{generation: 11}
	id := NodeID{BusName: "org.test.Private", ObjectPath: "/node", Generation: 11}
	for _, tc := range []struct {
		name string
		run  func() (Node, error)
		want string
	}{
		{"cached nil context", func() (Node, error) { return b.readNode(nil, id, NodeID{}, 128, false) }, "nil context"},
		{"fresh nil context", func() (Node, error) { return b.readNodeFresh(nil, id, NodeID{}, 128, false) }, "nil context"},
		{"cached disconnected", func() (Node, error) { return b.readNode(context.Background(), id, NodeID{}, 128, false) }, "disconnected"},
		{"fresh disconnected", func() (Node, error) { return b.readNodeFresh(context.Background(), id, NodeID{}, 128, false) }, "disconnected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := tc.run()
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) || node.ID != (NodeID{}) || node.Text != "" {
				t.Fatalf("untrusted provider yielded node=%+v err=%v", node, err)
			}
		})
	}
}

// Optional fields and unsupported destinations should not bypass provider
// authorization, even when the caller supplies a writable destination.
func TestNodeOptionalPropertiesRejectDisconnectedProvider(t *testing.T) {
	b := &dbusBackend{generation: 11}
	id := NodeID{BusName: "org.test.Private", ObjectPath: "/node", Generation: 11}
	var out float64
	if err := b.propertyFloat(context.Background(), id, "org.test.Private", "Value", &out); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("disconnected float provider accepted: %v", err)
	}
	node := Node{}
	b.readNodeComponent(context.Background(), id, []string{componentIface}, &node)
	if node.HasBounds || node.Bounds != (Rect{}) {
		t.Fatalf("untrusted geometry published: %+v", node)
	}
	if err := b.readNodeText(context.Background(), id, []string{textIface}, 128, &node); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("untrusted text provider accepted: %v", err)
	}
	if node.Text != "" {
		t.Fatalf("untrusted text published: %q", node.Text)
	}
}

func TestNilSensitiveNodeRedactionIsNoOp(t *testing.T) { redactSensitiveNode(nil) }
