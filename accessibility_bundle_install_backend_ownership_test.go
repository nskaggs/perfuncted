package perfuncted

import "testing"

// An accessibility backend offered after its bundle has disappeared, or to a
// bundle not managed by the generation owner, must be closed immediately.
// Otherwise its event subscription and bus connection could outlive authority.
type rejectedAccessibilityBackend struct {
	*bundleAccessibilityFake
	closes int
}

func (b *rejectedAccessibilityBackend) Close() error {
	b.closes++
	return nil
}

func TestAccessibilityInstallBackendClosesUnownedCandidates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		bundle *AccessibilityBundle
	}{
		{name: "nil bundle", bundle: nil},
		{name: "bundle without owner", bundle: &AccessibilityBundle{backend: &bundleAccessibilityFake{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := &rejectedAccessibilityBackend{bundleAccessibilityFake: &bundleAccessibilityFake{}}
			tc.bundle.installBackend(candidate)
			if candidate.closes != 1 {
				t.Fatalf("candidate close calls = %d, want one", candidate.closes)
			}
		})
	}
}
