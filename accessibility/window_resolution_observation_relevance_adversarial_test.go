package accessibility

import (
	"errors"
	"testing"
)

// Fresh window correlation is a snapshot transaction: provider changes that
// can invalidate the selected application's identity or observed candidates
// must force retry, while changes on unrelated applications must not.
func TestFreshWindowResolutionObservationChangeRelevanceMatrix(t *testing.T) {
	desktop := objectIdentity{busName: registryName, objectPath: "/desktop"}
	appID := objectIdentity{busName: "org.example.App", objectPath: "/app"}
	windowID := objectIdentity{busName: appID.busName, objectPath: "/window"}
	foreignID := objectIdentity{busName: "org.foreign.App", objectPath: "/other"}
	apps := []Application{{Node: Node{ID: NodeID{BusName: appID.busName, ObjectPath: appID.objectPath, Generation: 4}}}}
	observed := map[objectIdentity]Node{appID: apps[0].Node, windowID: {ID: NodeID{BusName: windowID.busName, ObjectPath: windowID.objectPath, Generation: 4}}}
	for _, tc := range []struct {
		name     string
		change   observationChange
		rejected bool
	}{
		{"unknown provider identity", observationChange{unknown: true}, true},
		{"desktop identity", observationChange{node: desktop}, true},
		{"desktop parent", observationChange{parent: desktop}, true},
		{"observed application", observationChange{node: appID}, true},
		{"observed window", observationChange{node: windowID}, true},
		{"new unknown child of application", observationChange{cacheAdd: true, node: objectIdentity{busName: appID.busName, objectPath: "/new-child"}, parent: appID}, true},
		{"removed observed window", observationChange{childrenRemove: true, removedChild: windowID, node: foreignID}, true},
		{"window creation same application", observationChange{kind: "org.a11y.atspi.Event.Window:Create", node: objectIdentity{busName: appID.busName, objectPath: "/new-window"}}, true},
		{"foreign unrelated property", observationChange{kind: "org.a11y.atspi.Event.Object:PropertyChange", node: foreignID}, false},
		{"foreign unrelated cache addition", observationChange{cacheAdd: true, node: foreignID}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := windowResolutionChangeError(tc.change, desktop, apps, observed)
			if tc.rejected && !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("stale window resolution was not rejected: %v", err)
			}
			if !tc.rejected && err != nil {
				t.Fatalf("unrelated application change rejected: %v", err)
			}
		})
	}
}

func TestFreshApplicationResolutionObservationChangeRelevanceMatrix(t *testing.T) {
	desktop := NodeID{BusName: registryName, ObjectPath: "/desktop", Generation: 4}
	app := Node{ID: NodeID{BusName: "org.example.App", ObjectPath: "/app", Generation: 4}}
	unrelated := objectIdentity{busName: "org.foreign.App", objectPath: "/unrelated"}
	for _, tc := range []struct {
		name   string
		change observationChange
		reject bool
	}{
		{"unknown identity", observationChange{unknown: true}, true},
		{"desktop itself", observationChange{node: objectIdentity{busName: desktop.BusName, objectPath: desktop.ObjectPath}}, true},
		{"desktop as parent", observationChange{parent: objectIdentity{busName: desktop.BusName, objectPath: desktop.ObjectPath}}, true},
		{"application itself", observationChange{node: objectIdentity{busName: app.ID.BusName, objectPath: app.ID.ObjectPath}}, true},
		{"application removed child", observationChange{removedChild: objectIdentity{busName: app.ID.BusName, objectPath: app.ID.ObjectPath}}, true},
		{"unknown removal within observed app", observationChange{cacheRemove: true, node: objectIdentity{busName: app.ID.BusName, objectPath: "/other"}}, true},
		{"foreign removal", observationChange{cacheRemove: true, node: unrelated}, false},
		{"unrelated property", observationChange{node: unrelated}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			change := tc.change
			change.revision = 2
			b := &dbusBackend{generation: 4, observationRevision: 2, observationHistory: []observationChange{change}}
			err := b.applicationResolutionChangesError(1, desktop, []Node{app})
			if tc.reject && !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("relevant application change accepted: %v", err)
			}
			if !tc.reject && err != nil {
				t.Fatalf("unrelated provider change rejected: %v", err)
			}
		})
	}
	b := &dbusBackend{generation: 4, observationRevision: 2, observationFloor: 2}
	if err := b.applicationResolutionChangesError(1, desktop, []Node{app}); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("expired observation history accepted: %v", err)
	}
	var missing *dbusBackend
	if err := missing.applicationResolutionChangesError(1, desktop, []Node{app}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend returned %v", err)
	}
}
