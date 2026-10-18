package perfuncted

import (
	"context"
	"slices"
	"testing"
)

func syntheticSupportedBundle() *bundleBase {
	return &bundleBase{
		capability: CapabilityInput,
		session: &Session{ctx: context.Background(), capabilities: map[Capability]CapabilityStatus{
			CapabilityInput: {
				Capability: CapabilityInput, Available: true, Backend: "*input.WlVirtualBackend",
				Operations:  []string{"keyboard", "click", "pointer", "scroll", "sync", "pointer-location", "pointer-coordinate-space"},
				Diagnostics: []string{"wayland", "keyboard active", "pointer active"},
			},
		}},
	}
}

// Screen, windows, and input all use this exact availability gate on every
// operation. The public Capability method must remain independently owned.
func BenchmarkSyntheticBundleCapabilityCheck(b *testing.B) {
	base := syntheticSupportedBundle()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := base.checkAvailable("keyboard", true); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBundleCapabilityLookupRetainsPublicSnapshotIsolation(t *testing.T) {
	b := syntheticSupportedBundle()
	if err := b.checkAvailable("keyboard", true); err != nil {
		t.Fatal(err)
	}
	if err := b.checkAvailable("not-supported", true); err == nil {
		t.Fatal("missing unsupported error")
	}
	if err := b.checkAvailable("keyboard", false); err == nil {
		t.Fatal("missing unavailable error")
	}
	if err := b.checkAvailable("keyboard", true); err != nil {
		t.Fatal(err)
	}
	snapshot := b.session.Capability(CapabilityInput)
	snapshot.Operations[0] = "mutated"
	snapshot.Diagnostics[0] = "mutated"
	if !slices.Contains(b.session.Capability(CapabilityInput).Operations, "keyboard") {
		t.Fatal("public Capability returned mutable session operations")
	}
	if err := b.checkAvailable("keyboard", true); err != nil {
		t.Fatalf("snapshot mutated checkAvailability: %v", err)
	}
}
