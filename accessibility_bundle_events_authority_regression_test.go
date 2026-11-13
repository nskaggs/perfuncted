package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type revokingAccessibilityEventSetup struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *revokingAccessibilityEventSetup) Events(_ context.Context, _ accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.calls++
	if b.after != nil {
		b.after()
	}
	events := make(chan accessibility.Event, 1)
	events <- accessibility.Event{Kind: "private-event"}
	return events, nil
}

func TestAccessibilityEventsRefuseUnauthorizedSubscriptionResults(t *testing.T) {
	for _, mode := range []string{"pre-cancel", "late-cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &revokingAccessibilityEventSetup{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			oldBackend := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = oldBackend })
			want := error(context.Canceled)
			switch mode {
			case "pre-cancel":
				cancel()
			case "late-cancel":
				backend.after = cancel
			case "session-close":
				want = ErrSessionClosed
				backend.after = func() {
					session.lifecycleMu.Lock()
					session.closed = true
					session.lifecycleMu.Unlock()
				}
			}
			stream, err := session.Accessibility.Events(ctx, accessibility.EventOptions{})
			wantCalls := 1
			if mode == "pre-cancel" {
				wantCalls = 0
			}
			if !errors.Is(err, want) || stream != nil || backend.calls != wantCalls {
				t.Fatalf("stream=%v err=%v calls=%d; want nil stream, %v and %d calls", stream, err, backend.calls, want, wantCalls)
			}
		})
	}
}
