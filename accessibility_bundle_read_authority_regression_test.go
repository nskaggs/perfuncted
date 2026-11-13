package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type revokingAccessibilityReads struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *revokingAccessibilityReads) Applications(context.Context) ([]accessibility.Application, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return []accessibility.Application{{Node: accessibility.Node{Name: "private-app"}}}, nil
}
func (b *revokingAccessibilityReads) Snapshot(_ context.Context, _ accessibility.NodeID, _ accessibility.SnapshotOptions) (accessibility.Snapshot, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return accessibility.Snapshot{Nodes: []accessibility.Node{{Name: "private-node"}}}, nil
}

func TestAccessibilityBundleReadsDiscardResultsAfterAuthorizationLoss(t *testing.T) {
	for _, operation := range []string{"applications", "snapshot"} {
		for _, mode := range []string{"pre-cancel", "late-cancel", "close"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				backend := &revokingAccessibilityReads{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
				session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
				t.Cleanup(func() { _ = session.Close() })
				wantErr := error(context.Canceled)
				if mode == "pre-cancel" {
					cancel()
				} else if mode == "late-cancel" {
					backend.after = cancel
				} else {
					wantErr = ErrSessionClosed
					backend.after = func() {
						session.lifecycleMu.Lock()
						session.closed = true
						session.lifecycleMu.Unlock()
					}
				}
				if operation == "applications" {
					got, err := session.Accessibility.Applications(ctx)
					if !errors.Is(err, wantErr) || len(got) != 0 {
						t.Errorf("apps result leaked: %v %v", got, err)
					}
				} else {
					got, err := session.Accessibility.Snapshot(ctx, accessibility.NodeID{}, accessibility.SnapshotOptions{})
					if !errors.Is(err, wantErr) || len(got.Nodes) != 0 {
						t.Errorf("snapshot result leaked: %+v %v", got, err)
					}
				}
				calls := 1
				if mode == "pre-cancel" {
					calls = 0
				}
				if backend.calls != calls {
					t.Errorf("backend calls = %d, want %d", backend.calls, calls)
				}
			})
		}
	}
}
