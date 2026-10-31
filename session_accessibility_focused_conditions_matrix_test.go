package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type focusedATSPISynthetic struct {
	waitAccessibilityBackend
	focused accessibility.Node
	err     error
	calls   int
}

func (b *focusedATSPISynthetic) Focused(ctx context.Context, _ accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.calls++
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	return b.focused, b.err
}

func TestAccessibilityFocusedConditionDistinguishesAbsentTargetErrorAndSuccess(t *testing.T) {
	sourceError := errors.New("synthetic AT-SPI bus read failed")
	for _, tc := range []struct {
		name string
		node accessibility.Node
		err  error
		want bool
	}{
		{"focused node", accessibility.Node{Focused: true, Name: "Search"}, nil, true},
		{"none focused", accessibility.Node{}, accessibility.ErrNotFound, false},
		{"disconnected provider", accessibility.Node{}, sourceError, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &focusedATSPISynthetic{focused: tc.node, err: tc.err}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
			t.Cleanup(func() { _ = session.Close() })
			condition := AccessibilityFocused(accessibility.SnapshotOptions{Fresh: true})
			ok, err := condition.evaluate(context.Background(), session)
			wantErr := tc.err
			if errors.Is(wantErr, accessibility.ErrNotFound) {
				wantErr = nil // absence is a pending state, not an observation failure
			}
			if ok != tc.want || !errors.Is(err, wantErr) {
				t.Fatalf("focus condition: ok=%t err=%v, expected ok=%t err=%v", ok, err, tc.want, wantErr)
			}
			if fake.calls != 1 {
				t.Fatalf("queried focus %d times", fake.calls)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if ok, err := condition.evaluate(canceled, session); ok || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled focused request: ok=%t err=%v", ok, err)
			}
		})
	}
}

func TestAccessibilityFocusedWaitOnlyCompletesOnAuthoritativeFocus(t *testing.T) {
	fake := &focusedATSPISynthetic{err: accessibility.ErrNotFound}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, fake)
	t.Cleanup(func() { _ = session.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := session.Wait(ctx, AccessibilityFocused(accessibility.SnapshotOptions{Fresh: true}), WaitEvery(2*time.Millisecond))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("missing focused node must not satisfy wait: %v", err)
	}
	if fake.calls < 2 {
		t.Fatalf("wait did not reread focus: %d queries", fake.calls)
	}
	fake.err = nil
	fake.focused = accessibility.Node{Focused: true}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := session.Wait(ctx2, AccessibilityFocused(accessibility.SnapshotOptions{Fresh: true}), WaitEvery(2*time.Millisecond)); err != nil {
		t.Fatalf("focused node did not satisfy wait: %v", err)
	}
}
