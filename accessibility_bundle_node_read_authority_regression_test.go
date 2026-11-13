package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type revokingAccessibilityNodeReads struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *revokingAccessibilityNodeReads) Focused(context.Context, accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Node{Name: "private-focused"}, nil
}
func (b *revokingAccessibilityNodeReads) AtPoint(context.Context, int, int) (accessibility.Node, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Node{Name: "private-point"}, nil
}

// Even if a provider completes successfully after session closure, the
// facade must not expose the focused/pointed accessible object's metadata.
func TestAccessibilityNodeReadsDoNotEscapeClosedSession(t *testing.T) {
	for _, name := range []string{"focused", "at-point"} {
		t.Run(name, func(t *testing.T) {
			backend := &revokingAccessibilityNodeReads{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			var node accessibility.Node
			var err error
			if name == "focused" {
				node, err = session.Accessibility.Focused(context.Background(), accessibility.SnapshotOptions{})
			} else {
				node, err = session.Accessibility.AtPoint(context.Background(), 14, 23)
			}
			if !errors.Is(err, ErrSessionClosed) || node.Name != "" || backend.calls != 1 {
				t.Fatalf("node leaked after session closure: node=%+v err=%v calls=%d", node, err, backend.calls)
			}
		})
	}
}
