package perfuncted

import (
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// Generation and invalidation are optional AT-SPI capabilities. A missing,
// typed-nil, or core-only provider must not pretend to expose a live handle
// epoch, and invalidation must never reach a hidden optional implementation.
func TestAccessibilityBundleGenerationAndInvalidationOptionalProviderBoundaries(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.example.Document", ObjectPath: "/document", Generation: 7}
	var absent *AccessibilityBundle
	if absent.Generation() != 0 {
		t.Fatal("nil bundle claimed live generation")
	}
	absent.Invalidate(id)

	var nilFake *bundleAccessibilityFake
	for _, tc := range []struct {
		name    string
		backend accessibility.Backend
	}{
		{name: "unconfigured"},
		{name: "typed nil", backend: nilFake},
		{name: "core only", backend: &coreOnlyAccessibilityBackend{Backend: &bundleAccessibilityFake{gen: 7}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := &AccessibilityBundle{backend: tc.backend}
			if got := bundle.Generation(); got != 0 {
				t.Fatalf("unsupported generation=%d, want zero", got)
			}
			bundle.Invalidate(id)
			if got := bundle.Generation(); got != 0 {
				t.Fatalf("unsupported invalidation advanced generation=%d", got)
			}
		})
	}
	provider := &bundleAccessibilityFake{gen: 7}
	bundle := &AccessibilityBundle{backend: provider}
	if got := bundle.Generation(); got != 7 {
		t.Fatalf("live provider generation=%d", got)
	}
	bundle.Invalidate(id)
	if got := bundle.Generation(); got != 8 {
		t.Fatalf("invalidation did not advance live generation=%d", got)
	}
}
