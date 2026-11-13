package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

type malformedEventSubscriptionBackend struct {
	*bundleAccessibilityFake
	stream <-chan accessibility.Event
	err    error
	calls  int
}

func (b *malformedEventSubscriptionBackend) Events(context.Context, accessibility.EventOptions) (<-chan accessibility.Event, error) {
	b.calls++
	return b.stream, b.err
}

// A failed subscription cannot publish a provider-owned stream; conversely
// success without a stream must not give consumers a permanently dead channel.
func TestAccessibilityBundleRejectsMalformedSubscriptionResults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stream     bool
		backendErr error
		want       error
	}{
		{name: "nil-stream", want: accessibility.ErrUnsupported},
		{name: "stream-with-error", stream: true, backendErr: errors.New("provider setup failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &malformedEventSubscriptionBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 1}, err: tc.backendErr}
			if tc.stream {
				backend.stream = make(chan accessibility.Event, 1)
			}
			session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
			t.Cleanup(func() { _ = session.Close() })
			owner := session.Accessibility.backend
			session.Accessibility.backend = backend
			t.Cleanup(func() { session.Accessibility.backend = owner })
			stream, err := session.Accessibility.Events(context.Background(), accessibility.EventOptions{})
			want := tc.want
			if want == nil {
				want = tc.backendErr
			}
			if stream != nil || !errors.Is(err, want) || backend.calls != 1 {
				t.Fatalf("%s malformed provider result published stream=%v err=%v calls=%d", tc.name, stream, err, backend.calls)
			}
		})
	}
}
