package perfuncted

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func syntheticSupportedBundle() *bundleBase {
	bundle := &bundleBase{
		capability: CapabilityInput,
		session: &Session{ctx: context.Background(), capabilities: map[Capability]CapabilityStatus{
			CapabilityInput: {
				Capability: CapabilityInput, Available: true, Backend: "*input.WlVirtualBackend",
				Operations:  []string{"keyboard", "click", "pointer", "scroll", "sync", "pointer-location", "pointer-coordinate-space"},
				Diagnostics: []string{"wayland", "keyboard active", "pointer active"},
			},
		}},
	}
	bundle.cacheOperations(bundle.session.capabilities[CapabilityInput].Operations)
	return bundle
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

// The metadata snapshot can be updated or returned to callers without changing
// the operation contract cached when a normal session opens.
func TestSessionStaticOperationContractRemainsFixed(t *testing.T) {
	b := syntheticSupportedBundle()
	b.session.capabilitiesMu.Lock()
	status := b.session.capabilities[CapabilityInput]
	status.Operations = []string{"different-operation"}
	b.session.capabilities[CapabilityInput] = status
	b.session.capabilitiesMu.Unlock()
	if err := b.checkAvailable("keyboard", true); err != nil {
		t.Fatalf("cached keyboard support unexpectedly disappeared: %v", err)
	}
	if err := b.checkAvailable("different-operation", true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unadvertised operation unexpectedly succeeded: %v", err)
	}
	b.session.lifecycleMu.Lock()
	b.session.closed = true
	b.session.lifecycleMu.Unlock()
	if err := b.checkAvailable("keyboard", true); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("closed session returned %v, want ErrSessionClosed", err)
	}
}

func TestAccessibilityOperationSupportRemainsDynamic(t *testing.T) {
	b := syntheticSupportedBundle()
	b.capability = CapabilityAccessibility
	b.cacheOperations([]string{"find"}) // must be ignored for dynamic accessibility
	b.session.capabilities[CapabilityAccessibility] = CapabilityStatus{
		Capability: CapabilityAccessibility,
		Operations: []string{"find"},
		Available:  true,
	}
	if err := b.checkAvailable("find", true); err != nil {
		t.Fatal(err)
	}
	b.session.capabilitiesMu.Lock()
	status := b.session.capabilities[CapabilityAccessibility]
	status.Operations = []string{"snapshot"}
	b.session.capabilities[CapabilityAccessibility] = status
	b.session.capabilitiesMu.Unlock()
	if err := b.checkAvailable("find", true); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("stale accessibility support was used: %v", err)
	}
	if err := b.checkAvailable("snapshot", true); err != nil {
		t.Fatalf("newly available accessibility operation rejected: %v", err)
	}
}

// Normal session construction wires the immutable contracts before returning.
// Accessibility must remain on the live path because its backend may reopen.
func TestSessionCreationCachesOnlyStaticOperations(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	for _, bundle := range []*bundleBase{
		&session.Screen.bundleBase,
		&session.Input.bundleBase,
		&session.Windows.bundleBase,
		&session.Outputs.bundleBase,
		&session.Clipboard.bundleBase,
	} {
		if !bundle.operationsResolved {
			t.Fatalf("%s was not prevalidated at session creation", bundle.capability)
		}
	}
	if session.Accessibility.operationsResolved {
		t.Fatal("accessibility cannot prevalidate a reopenable backend")
	}
}
