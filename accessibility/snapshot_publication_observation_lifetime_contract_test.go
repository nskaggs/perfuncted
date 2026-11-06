package accessibility

import (
	"errors"
	"testing"
	"time"
)

// Publishing a snapshot is the last authorization boundary. An object that
// disappeared, an inaccessible backend, or an intervening provider event must
// prevent the snapshot from becoming a reusable cache entry.
func TestSnapshotPublicationRejectsLostBackendRevokedIdentityAndChangedObservations(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/window", Generation: 7}
	rootIdentity := objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}
	observation := observationChange{revision: 4, node: rootIdentity, kind: "org.a11y.atspi.Event.Object:PropertyChange"}
	for _, tc := range []struct {
		name      string
		configure func(*dbusBackend)
		want      error
	}{
		{"backend closed", func(b *dbusBackend) { b.closed = true }, ErrDisconnected},
		{"provider disconnected", func(b *dbusBackend) { b.disconnected = true }, ErrDisconnected},
		{"object path reincarnated", func(b *dbusBackend) { b.incarnations = map[objectIdentity]uint64{rootIdentity: 2} }, ErrStaleNode},
		{"intervening property change", func(b *dbusBackend) {
			b.observationRevision = 4
			b.observationHistory = []observationChange{observation}
		}, ErrObservationChanged},
		{"unverifiable history", func(b *dbusBackend) { b.observationRevision = 4; b.observationFloor = 4 }, ErrObservationChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, observationRevision: 3}
			tc.configure(b)
			snap := Snapshot{Root: Node{ID: root, Name: "stale editor"}, Nodes: []Node{{ID: root, Name: "stale editor"}}, Generation: 7, CapturedAt: time.Now()}
			err := b.publishSnapshot("candidate", 7, 3, root, true, snap)
			if !errors.Is(err, tc.want) {
				t.Fatalf("unverifiable snapshot publication returned %v, want %v", err, tc.want)
			}
			if len(b.cache) != 0 {
				t.Fatalf("unverifiable snapshot entered reusable cache: %+v", b.cache)
			}
		})
	}
}

func TestSnapshotPublishedCacheOwnsProviderData(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/window", Generation: 7}
	child := NodeID{BusName: root.BusName, ObjectPath: "/window/confirm", Generation: 7}
	b := &dbusBackend{generation: 7}
	snap := Snapshot{Root: Node{ID: root, Name: "Editor"}, Nodes: []Node{
		{ID: root, Name: "Editor", Children: []NodeID{child}},
		{ID: child, Name: "Confirm", Attributes: map[string]string{"action": "confirm"}, States: []string{"enabled"}},
	}, Generation: 7, CapturedAt: time.Now()}
	if err := b.publishSnapshot("verified", 7, 0, root, true, snap); err != nil {
		t.Fatalf("verified snapshot rejected: %v", err)
	}
	snap.Nodes[0].Children[0] = NodeID{}
	snap.Nodes[1].Attributes["action"] = "cancel"
	snap.Nodes[1].States[0] = "disabled"
	cached, ok := b.cache["verified"]
	if !ok || len(cached.snapshot.Nodes) != 2 {
		t.Fatalf("verified cache missing: %+v", cached)
	}
	if cached.snapshot.Nodes[0].Children[0] != child || cached.snapshot.Nodes[1].Attributes["action"] != "confirm" || cached.snapshot.Nodes[1].States[0] != "enabled" {
		t.Fatalf("published snapshot aliases mutable provider data: %+v", cached.snapshot.Nodes)
	}
}

// Fresh application and window lookups cannot trust a finite event history
// after its relevant entries have been evicted. Empty intervals, by contrast,
// are safe even when older unrelated events remain in the ring buffer.
func TestWindowResolutionRejectsExpiredObservationHistoryWithoutLeakingCandidates(t *testing.T) {
	desktop := NodeID{BusName: registryName, ObjectPath: string(desktopPath), Generation: 5}
	app := Application{Node: Node{ID: NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 5}}}
	window := Node{ID: NodeID{BusName: app.ID.BusName, ObjectPath: "/app/window", Generation: 5}, Role: "frame", Name: "Editor"}
	for _, tc := range []struct {
		name     string
		backend  *dbusBackend
		expected uint64
		want     error
	}{
		{"unavailable backend", nil, 1, ErrDisconnected},
		{"evicted history", &dbusBackend{generation: 5, observationRevision: 8, observationFloor: 7}, 6, ErrObservationChanged},
		{"matching revision", &dbusBackend{generation: 5, observationRevision: 8, observationFloor: 7}, 8, nil},
		{"only preceding events", &dbusBackend{generation: 5, observationRevision: 8, observationHistory: []observationChange{{revision: 3, unknown: true}}}, 8, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.backend.windowResolutionChangesError(tc.expected, desktop, []Application{app}, []Node{window})
			if !errors.Is(err, tc.want) {
				t.Fatalf("window resolution consistency error=%v, want %v", err, tc.want)
			}
		})
	}
}
