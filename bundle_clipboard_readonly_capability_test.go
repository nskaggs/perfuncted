package perfuncted

import (
	"context"
	"errors"
	"testing"
)

// The provider may be readable while explicitly lacking permission to write.
// Capability admission must reject Set without dispatching a mutation.
type readOnlyClipboardFixture struct{ *authorityLossClipboardBackend }

func (*readOnlyClipboardFixture) SupportedOperations() []string { return []string{"get"} }

func TestClipboardSetRejectsReadOnlyBackendBeforeMutation(t *testing.T) {
	backend := &readOnlyClipboardFixture{authorityLossClipboardBackend: &authorityLossClipboardBackend{}}
	session := NewSessionForTesting(nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	if _, err := session.Clipboard.Get(context.Background()); err != nil {
		t.Fatalf("readable clipboard not available: %v", err)
	}
	err := session.Clipboard.Set(context.Background(), "private content")
	if !errors.Is(err, ErrUnsupported) || backend.calls != 0 {
		t.Fatalf("read-only clipboard accepted mutation: err=%v backend calls=%d", err, backend.calls)
	}
}
