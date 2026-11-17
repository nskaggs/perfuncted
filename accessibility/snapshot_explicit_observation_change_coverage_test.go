package accessibility

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestExplicitSnapshotRejectsObservedObjectChangeDuringTraversal(t *testing.T) {
	var backend *dbusBackend
	var root NodeID
	backendReady := make(chan struct{})
	backend, root, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/app")}}
		frame.childrenHook = func() {
			<-backendReady
			signal := &dbus.Signal{
				Name:   "org.a11y.atspi.Event.Object.PropertyChange",
				Sender: registryName,
				Path:   dbus.ObjectPath("/org/a11y/atspi/accessible/editor"),
				Body:   []any{"Name", "Editor changed during snapshot"},
			}
			backend.prepareEventTransition(signal, signalEvent(signal))
		}
	})
	close(backendReady)
	defer backend.Close()

	snapshot, err := backend.Snapshot(context.Background(), root, SnapshotOptions{Fresh: true})
	if !errors.Is(err, ErrObservationChanged) || len(snapshot.Nodes) != 0 || snapshot.Root.ID.valid() {
		t.Fatalf("explicit snapshot exposed data across a provider change: nodes=%d root=%+v err=%v", len(snapshot.Nodes), snapshot.Root, err)
	}
	backend.mu.RLock()
	cacheEntries := len(backend.cache)
	backend.mu.RUnlock()
	if cacheEntries != 0 {
		t.Fatalf("unstable explicit snapshot published %d cache entries", cacheEntries)
	}
}
