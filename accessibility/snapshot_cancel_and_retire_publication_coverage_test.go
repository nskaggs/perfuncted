package accessibility

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

func TestSnapshotCancellationDuringProviderTraversalDiscardsPartialNodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/child"}}
		frame.childrenHook = func() { cancel() }
	})

	snapshot, err := backend.Snapshot(ctx, root, SnapshotOptions{Fresh: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot after provider-boundary cancellation = %v", err)
	}
	if len(snapshot.Nodes) != 0 || snapshot.Root.ID.valid() {
		t.Fatalf("canceled snapshot exposed partial traversal: %+v", snapshot)
	}
	if len(backend.cache) != 0 {
		t.Fatalf("canceled snapshot published %d cache entries", len(backend.cache))
	}
}

func TestSnapshotDoesNotPublishAfterBackendRetiresDuringTraversal(t *testing.T) {
	var liveBackend atomic.Pointer[dbusBackend]
	var root NodeID
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/child"}}
		frame.childrenHook = func() {
			current := liveBackend.Load()
			_ = current.Close()
		}
	})
	liveBackend.Store(backend)

	snapshot, err := backend.Snapshot(context.Background(), root, SnapshotOptions{Fresh: true})
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("snapshot published after backend retirement: %v", err)
	}
	if len(snapshot.Nodes) != 0 || snapshot.Root.ID.valid() {
		t.Fatalf("retired backend exposed unpublished nodes: %+v", snapshot)
	}
	if len(backend.cache) != 0 {
		t.Fatalf("retired backend published %d cache entries", len(backend.cache))
	}
}
