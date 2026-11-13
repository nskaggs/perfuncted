package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type revokingAccessibilityMutations struct {
	*accessibilityAutomationFake
	after func()
	calls int
}

func (b *revokingAccessibilityMutations) InvokeAction(context.Context, accessibility.NodeID, int32) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *revokingAccessibilityMutations) ScrollTo(context.Context, accessibility.NodeID, accessibility.ScrollType) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *revokingAccessibilityMutations) SetTextContents(context.Context, accessibility.NodeID, string) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *revokingAccessibilityMutations) SelectAll(context.Context, accessibility.NodeID) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}

// AT-SPI mutations must never dispatch on a canceled request or report
// success after the caller or owning session revoked authority, even when
// the provider ignores its context and returns nil.
func TestAccessibilityMutationsRespectAuthorityAtDispatchAndReceipt(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.synthetic", ObjectPath: "/element", Generation: 1}
	actions := []struct {
		name string
		run  func(*AccessibilityBundle, context.Context) error
	}{
		{"invoke-action", func(b *AccessibilityBundle, c context.Context) error { return b.InvokeAction(c, id, 1) }},
		{"scroll", func(b *AccessibilityBundle, c context.Context) error {
			return b.ScrollTo(c, id, accessibility.ScrollAnyWhere)
		}},
		{"set-text-contents", func(b *AccessibilityBundle, c context.Context) error {
			return b.ReplaceEditableText(c, id, "private text")
		}},
		{"select-all", func(b *AccessibilityBundle, c context.Context) error { return b.SelectAll(c, id) }},
	}
	for _, action := range actions {
		for _, mode := range []string{"pre-cancel", "late-cancel", "session-close"} {
			t.Run(action.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				b := &revokingAccessibilityMutations{accessibilityAutomationFake: &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, accessibilityAutomationSpy: &accessibilityAutomationSpy{}}}
				session := NewSessionForTesting(nil, nil, nil, nil, nil, b)
				t.Cleanup(func() { _ = session.Close() })
				owner := session.Accessibility.backend
				session.Accessibility.backend = b
				t.Cleanup(func() { session.Accessibility.backend = owner })
				expected := error(context.Canceled)
				switch mode {
				case "pre-cancel":
					cancel()
				case "late-cancel":
					b.after = cancel
				case "session-close":
					expected = ErrSessionClosed
					b.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
				}
				err := action.run(session.Accessibility, ctx)
				wantCalls := 1
				if mode == "pre-cancel" {
					wantCalls = 0
				}
				if !errors.Is(err, expected) || b.calls != wantCalls {
					t.Fatalf("error=%v calls=%d; want error %v calls %d", err, b.calls, expected, wantCalls)
				}
			})
		}
	}
}
