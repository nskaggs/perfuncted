package accessibility

import (
	"errors"
	"strconv"
	"testing"
)

// A complete incarnation map leaves no capacity to record a new removed
// object. Selective revocation must refuse to claim a complete subtree so
// the caller can retire the entire backend epoch instead.
func TestAccessibilitySelectiveRevocationRejectsUntrackedObjectAtCapacity(t *testing.T) {
	const bus = "org.example.Editor"
	b := &dbusBackend{generation: 7, incarnations: make(map[objectIdentity]uint64, maxTrackedObjects)}
	for i := 0; i < maxTrackedObjects; i++ {
		b.incarnations[objectIdentity{busName: bus, objectPath: "/observed/" + strconv.Itoa(i)}] = 1
	}
	removed := objectIdentity{busName: bus, objectPath: "/newly-removed"}
	if b.revokeObjectLocked(removed) {
		t.Fatal("selective revoke accepted object with no remaining tracking capacity")
	}
	if b.generation != 7 || len(b.incarnations) != maxTrackedObjects || b.incarnations[removed] != 0 {
		t.Fatalf("refused selective revoke mutated epoch or tracking: gen=%d count=%d new=%d", b.generation, len(b.incarnations), b.incarnations[removed])
	}
	// Once called through the normal policy, an untracked removal must
	// invalidate the entire epoch rather than preserving old handles.
	b.revokeOrInvalidateLocked(removed)
	if b.generation != 8 || len(b.incarnations) != 0 || b.parents != nil {
		t.Fatalf("untracked removal failed closed: generation=%d identities=%d parents=%v", b.generation, len(b.incarnations), b.parents)
	}
}

func TestAccessibilityRejectsMalformedHandleAndNilEnumerationContext(t *testing.T) {
	b := &dbusBackend{generation: 7}
	if err := b.validateHandle(NodeID{BusName: "org.example.Editor", ObjectPath: "", Generation: 7}); !errors.Is(err, ErrStaleNode) {
		t.Fatalf("malformed handle accepted: %v", err)
	}
	apps, err := b.Applications(nil)
	if err == nil || len(apps) != 0 {
		t.Fatalf("nil context disclosed applications: %v err=%v", apps, err)
	}
}
