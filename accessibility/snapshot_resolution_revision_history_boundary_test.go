package accessibility

import (
	"errors"
	"testing"
)

// A fresh resolution reads an observation revision before gathering provider
// identities. Earlier events are already represented by those identities;
// treating them as concurrent changes would reject an otherwise coherent
// result. Later relevant events must still invalidate that same result.
func TestFreshResolutionOnlyInvalidatesForChangesAfterCapturedRevision(t *testing.T) {
	const generation = 5
	desktop := NodeID{BusName: registryName, ObjectPath: "/desktop", Generation: generation}
	app := Node{ID: NodeID{BusName: "org.example.App", ObjectPath: "/app", Generation: generation}}
	window := Node{ID: NodeID{BusName: app.ID.BusName, ObjectPath: "/window", Generation: generation}, Role: "frame"}
	desktopIdentity := objectIdentity{busName: desktop.BusName, objectPath: desktop.ObjectPath}
	windowIdentity := objectIdentity{busName: window.ID.BusName, objectPath: window.ID.ObjectPath}
	foreign := objectIdentity{busName: "org.other.App", objectPath: "/other"}
	for _, tc := range []struct {
		name        string
		later       observationChange
		wantChanged bool
	}{
		{name: "older unknown event ignored", later: observationChange{node: foreign}},
		{name: "later observed window change invalidates", later: observationChange{node: windowIdentity}, wantChanged: true},
		{name: "later desktop change invalidates", later: observationChange{node: desktopIdentity}, wantChanged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &dbusBackend{generation: generation, observationRevision: 3, observationHistory: []observationChange{
				{revision: 1, unknown: true},
				{revision: 2, unknown: true},
				{revision: 3, cacheRemove: true, node: desktopIdentity},
			}}
			backend.observationHistory = append(backend.observationHistory, observationChange{revision: 4, node: tc.later.node, parent: tc.later.parent, cacheRemove: tc.later.cacheRemove})
			backend.observationRevision = 4
			expected := uint64(3)
			// Window candidates resolved from app ownership may be invalidated by
			// candidate changes or desktop application list changes, not foreign apps.
			winErr := backend.windowResolutionChangesError(expected, desktop, []Application{{Node: app}}, []Node{window})
			if errors.Is(winErr, ErrObservationChanged) != tc.wantChanged || (winErr != nil && !tc.wantChanged) {
				t.Fatalf("window resolution history validation=%v changed=%t", winErr, tc.wantChanged)
			}
			// Application resolution observes only app and desktop identities.
			appErr := backend.applicationResolutionChangesError(expected, desktop, []Node{app})
			wantAppChanged := tc.later.node == desktopIdentity
			if errors.Is(appErr, ErrObservationChanged) != wantAppChanged || (appErr != nil && !wantAppChanged) {
				t.Fatalf("application resolution history validation=%v changed=%t", appErr, wantAppChanged)
			}
		})
	}
}
