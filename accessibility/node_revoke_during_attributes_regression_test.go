package accessibility

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// A provider can invalidate a node while its optional attributes are being
// read. The final handle validation must reject the partial observation even
// when its backend generation remains unchanged.
func TestNodeFreshReadRejectsRevocationDuringAttributeReply(t *testing.T) {
	var backend *dbusBackend
	var once sync.Once
	var invalidated atomic.Bool
	ready := make(chan struct{})
	var id NodeID
	var frameIdentity objectIdentity
	backend, id, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.attributesHook = func() {
			<-ready
			once.Do(func() {
				backend.mu.Lock()
				invalidated.Store(backend.revokeObjectLocked(frameIdentity))
				backend.mu.Unlock()
			})
		}
	})
	frameIdentity = objectIdentity{busName: id.BusName, objectPath: id.ObjectPath}
	close(ready)
	oldGeneration := backend.Generation()
	node, err := backend.readNodeFresh(context.Background(), id, NodeID{}, 40, true)
	if !invalidated.Load() || backend.Generation() != oldGeneration {
		t.Fatalf("expected object-only revocation, invalidated=%v generation=%d", invalidated.Load(), backend.Generation())
	}
	if !errors.Is(err, ErrStaleNode) || node.ID != (NodeID{}) || node.Text != "" {
		t.Fatalf("revoked node observation leaked: node=%+v err=%v", node, err)
	}
}
