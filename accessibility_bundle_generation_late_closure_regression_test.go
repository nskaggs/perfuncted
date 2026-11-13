package perfuncted

import "testing"

type closingGenerationReadBackend struct {
	*bundleAccessibilityFake
	after func()
	calls int
}

func (b *closingGenerationReadBackend) Generation() uint64 {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return 71
}

// A generation read is an authorization-sensitive provider identity, and
// cannot remain valid if the session closes while the backend is reading it.
func TestAccessibilityGenerationDiscardsIdentityOnLateSessionClosure(t *testing.T) {
	backend := &closingGenerationReadBackend{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 71}}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	t.Cleanup(func() { _ = session.Close() })
	owner := session.Accessibility.backend
	session.Accessibility.backend = backend
	t.Cleanup(func() { session.Accessibility.backend = owner })
	backend.after = func() { session.lifecycleMu.Lock(); session.closed = true; session.lifecycleMu.Unlock() }
	got := session.Accessibility.Generation()
	if got != 0 || backend.calls != 1 {
		t.Fatalf("generation leaked during closure: got=%d calls=%d", got, backend.calls)
	}
}
