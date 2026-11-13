package accessibility

import (
	"errors"
	"testing"
)

// An absent byte limit must not expose cached source storage through a
// response. Caller mutations are permitted only on the owned projection.
func TestSnapshotUnboundedResponseClonesOwnedNodesAndRoot(t *testing.T) {
	for _, budget := range []int{0, -1} {
		t.Run(map[int]string{0: "zero means unlimited", -1: "negative means unlimited"}[budget], func(t *testing.T) {
			original := Snapshot{Root: Node{Name: "original-root", Children: []NodeID{{BusName: "org.test", ObjectPath: "/child"}}}, Nodes: []Node{
				{Name: "source", Children: []NodeID{{BusName: "org.test", ObjectPath: "/child"}}, Interfaces: []string{"Accessible"}},
				{Name: "child", Interfaces: []string{"Action"}},
			}}
			got, err := boundSnapshotResponse(original, budget)
			if err != nil || len(got.Nodes) != 2 || got.Root.Name != "source" || got.Truncated {
				t.Fatalf("unbounded response lost original nodes: %+v err=%v", got, err)
			}
			got.Nodes[0].Name = "mutated"
			got.Nodes[0].Children[0].ObjectPath = "/changed"
			got.Nodes[1].Interfaces[0] = "changed"
			got.Root.Name = "mutated-root"
			if original.Nodes[0].Name != "source" || original.Nodes[0].Children[0].ObjectPath != "/child" || original.Nodes[1].Interfaces[0] != "Action" || original.Root.Name != "original-root" {
				t.Fatalf("unbounded response aliased source snapshot: %+v", original)
			}
		})
	}
}

// A removed candidate can be relevant even when no application identity was
// resolved for its bus. Retaining it would allow stale window selection.
func TestFreshWindowResolutionRejectsRemovedOrphanCandidate(t *testing.T) {
	desktop := objectIdentity{busName: registryName, objectPath: "/desktop"}
	orphan := objectIdentity{busName: "org.example.Orphan", objectPath: "/window"}
	observed := map[objectIdentity]Node{orphan: {ID: NodeID{BusName: orphan.busName, ObjectPath: orphan.objectPath}}}
	change := observationChange{childrenRemove: true, node: objectIdentity{busName: "org.other", objectPath: "/parent"}, parent: objectIdentity{busName: "org.other", objectPath: "/parent"}, removedChild: orphan}
	if err := windowResolutionChangeError(change, desktop, nil, observed); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("removed orphan window still eligible for fresh resolution: %v", err)
	}
	change.removedChild = objectIdentity{busName: "org.other", objectPath: "/unrelated"}
	if err := windowResolutionChangeError(change, desktop, nil, observed); err != nil {
		t.Fatalf("unrelated removed child invalidated orphan candidate: %v", err)
	}
}

func TestSnapshotObservationChangesNilBackendIsDisconnected(t *testing.T) {
	var backend *dbusBackend
	if err := backend.observationChangesError(1, NodeID{}, Snapshot{}, true); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil observation backend error=%v", err)
	}
}
