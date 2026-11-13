package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"testing"
)

type closingOutlineReadBackend struct {
	*bundleAccessibilityFake
	after  func()
	direct bool
	calls  int
}

func (b *closingOutlineReadBackend) SupportedOperations() []string {
	return append(b.bundleAccessibilityFake.SupportedOperations(), "outline")
}
func (b *closingOutlineReadBackend) Outline(context.Context, accessibility.NodeID, accessibility.SnapshotOptions, accessibility.OutlineOptions) (accessibility.Outline, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Outline{Warnings: []string{"private outline"}}, nil
}

type closingOutlineFallbackBackend struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *closingOutlineFallbackBackend) SupportedOperations() []string {
	return append(b.bundleAccessibilityFake.SupportedOperations(), "outline")
}
func (b *closingOutlineFallbackBackend) Snapshot(context.Context, accessibility.NodeID, accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Snapshot{Nodes: []accessibility.Node{{Name: "private node"}}}, nil
}

// Neither a provider-supplied outline nor an outline built from a snapshot
// may escape after the owning session has closed during backend execution.
func TestAccessibilityOutlineDiscardsResultAfterSessionClosure(t *testing.T) {
	for _, name := range []string{"provider", "fallback"} {
		t.Run(name, func(t *testing.T) {
			var backend accessibility.Backend
			var after *func()
			var calls *int
			if name == "provider" {
				probe := &closingOutlineReadBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
				backend = probe
				after = &probe.after
				calls = &probe.calls
			} else {
				probe := &closingOutlineFallbackBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
				backend = probe
				after = &probe.after
				calls = &probe.calls
			}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			*after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
			result, err := session.Accessibility.Outline(context.Background(), accessibility.NodeID{}, accessibility.SnapshotOptions{}, accessibility.OutlineOptions{})
			if !errors.Is(err, ErrSessionClosed) || len(result.Warnings) != 0 || len(result.Root.Children) != 0 || *calls != 1 {
				t.Fatalf("outline after closure: result=%+v error=%v backend calls=%d", result, err, *calls)
			}
		})
	}
}
